// mediastore.go: HUI-3553 受控真实媒体存储。
//
// 纪律(票面/裁定书):
//   - 上传完成 = 实际文件字节已落盘且通过验证(MIME/长度/哈希);仅登记引用
//     不算上传完成;
//   - 服务器以真实资产元数据决定类型:魔数探测 MIME,绝不采信客户端伪填;
//   - mp4 的时长/宽高来自对盒子结构的真实解析(mvhd/tkhd,尽力而为,缺失
//     记 0,绝不编造);
//   - 文件内容寻址落盘 <dir>/<sha256 前两位>/<sha256><ext>,库中零字节;
//     同字节重复上传去重复用(平台重复取材零新增);
//   - 大小上限强制(fail-closed),未知魔数拒绝。

package upload

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MediaKind is the server-detected media class.
type MediaKind string

const (
	KindVideo MediaKind = "video"
	KindImage MediaKind = "image"
)

// Sentinel errors (fail-closed classification).
var (
	// ErrMediaNotFound means the controlled store has no file for this sha.
	ErrMediaNotFound = errors.New("upload: media file not found")
	// ErrUnsupportedMedia means the bytes carry no whitelisted magic number.
	ErrUnsupportedMedia = errors.New("upload: unsupported media bytes")
	// ErrMediaTooLarge means the upload exceeded the configured cap.
	ErrMediaTooLarge = errors.New("upload: media exceeds size cap")
)

// MediaMeta is what one verified upload proves about its bytes.
type MediaMeta struct {
	SHA256     string    `json:"sha256"`
	MIME       string    `json:"mime_type"`
	Kind       MediaKind `json:"kind"`
	ByteSize   int64     `json:"byte_size"`
	DurationMS int64     `json:"duration_ms"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	MediaRef   string    `json:"media_ref"`
	Replayed   bool      `json:"replayed"`
}

// MediaStore keeps real file bytes on local controlled storage, content-
// addressed by sha256. Bytes never enter the database.
type MediaStore struct {
	Dir      string
	MaxBytes int64
}

// NewMediaStore builds a store rooted at dir with a hard upload cap.
func NewMediaStore(dir string, maxBytes int64) *MediaStore {
	return &MediaStore{Dir: dir, MaxBytes: maxBytes}
}

// sniffMedia decides MIME/kind from leading magic bytes only.
func sniffMedia(head []byte) (string, MediaKind, string, error) {
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg", KindImage, ".jpg", nil
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", KindImage, ".png", nil
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp", KindImage, ".webp", nil
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		brand := string(head[8:12])
		if strings.HasPrefix(brand, "qt") {
			return "video/quicktime", KindVideo, ".mov", nil
		}
		return "video/mp4", KindVideo, ".mp4", nil
	default:
		return "", "", "", ErrUnsupportedMedia
	}
}

// Save streams the upload to disk while hashing, sniffs the real MIME from
// the leading bytes, probes mp4 structure for duration/dimensions, and lands
// the file content-addressed. Same bytes twice = same ref (replayed).
func (s *MediaStore) Save(r io.Reader) (MediaMeta, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return MediaMeta{}, err
	}
	tmp, err := os.CreateTemp(s.Dir, "media-upload-*")
	if err != nil {
		return MediaMeta{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename

	head := make([]byte, 64)
	headN, headErr := io.ReadFull(r, head)
	if headErr != nil && headErr != io.ErrUnexpectedEOF {
		tmp.Close()
		return MediaMeta{}, headErr
	}
	mime, kind, ext, sniffErr := sniffMedia(head[:headN])
	if sniffErr != nil {
		tmp.Close()
		return MediaMeta{}, sniffErr
	}
	if _, err := tmp.Write(head[:headN]); err != nil {
		tmp.Close()
		return MediaMeta{}, err
	}

	digest := newSHA()
	digest.Write(head[:headN])
	var total int64 = int64(headN)
	buf := make([]byte, 64<<10)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			total += int64(n)
			if s.MaxBytes > 0 && total > s.MaxBytes {
				tmp.Close()
				return MediaMeta{}, ErrMediaTooLarge
			}
			digest.Write(buf[:n])
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				tmp.Close()
				return MediaMeta{}, werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			tmp.Close()
			return MediaMeta{}, rerr
		}
	}
	if err := tmp.Close(); err != nil {
		return MediaMeta{}, err
	}

	sha := digest.Hex()
	target := s.Path(sha, ext)
	if _, err := os.Stat(target); err == nil {
		return MediaMeta{
			SHA256: sha, MIME: mime, Kind: kind, ByteSize: total,
			MediaRef: sha, Replayed: true,
		}, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return MediaMeta{}, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return MediaMeta{}, err
	}
	meta := MediaMeta{
		SHA256: sha, MIME: mime, Kind: kind, ByteSize: total,
		MediaRef: sha,
	}
	if mime == "video/mp4" {
		probeMP4(head[:headN], target, &meta)
	}
	return meta, nil
}

// Path renders the content-addressed storage location.
func (s *MediaStore) Path(sha256Hex, ext string) string {
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return filepath.Join(s.Dir, sha256Hex[:2], sha256Hex+ext)
}

// Open returns the stored bytes for reading (controlled media GET). Unknown
// content is ErrMediaNotFound (uniform; no existence oracle beyond that).
func (s *MediaStore) Open(sha256Hex, ext string) (*os.File, error) {
	sha256Hex = strings.ToLower(strings.TrimSpace(sha256Hex))
	if len(sha256Hex) != 64 {
		return nil, ErrMediaNotFound
	}
	f, err := os.Open(s.Path(sha256Hex, ext))
	if err != nil {
		return nil, ErrMediaNotFound
	}
	return f, nil
}

// ---- hashing helper -----------------------------------------------------------------

type sha256Hex struct {
	h hash.Hash
}

func newSHA() *sha256Hex { return &sha256Hex{h: sha256.New()} }

func (d *sha256Hex) Write(p []byte) (int, error) { return d.h.Write(p) }
func (d *sha256Hex) Hex() string                 { return fmt.Sprintf("%x", d.h.Sum(nil)) }

// ---- mp4 structure probe (mvhd duration, tkhd dimensions) ----------------------------
//
// 尽力而为:盒子缺失/截断一律记 0,绝不编造;解析失败不影响已落盘文件。

func probeMP4(head []byte, path string, meta *MediaMeta) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = head // reserved for future partial-parse fast path
	var timescale, duration uint64
	var maxW, maxH uint32
	walkBoxes(data, 0, int64(len(data)), func(boxType string, body []byte) bool {
		switch boxType {
		case "moov":
			walkBoxes(body, 0, int64(len(body)), func(inner string, innerBody []byte) bool {
				switch inner {
				case "mvhd":
					timescale, duration = parseMVHD(innerBody)
				case "trak":
					// tkhd lives at moov/trak/tkhd — descend one more level.
					walkBoxes(innerBody, 0, int64(len(innerBody)), func(t string, tb []byte) bool {
						if t == "tkhd" {
							w, h := parseTKHD(tb)
							if w > maxW {
								maxW = w
							}
							if h > maxH {
								maxH = h
							}
						}
						return true
					})
				}
				return true
			})
			return false
		}
		return true
	})
	if timescale > 0 {
		meta.DurationMS = int64(duration * 1000 / timescale)
	}
	meta.Width = int(maxW >> 16) // 16.16 fixed point
	meta.Height = int(maxH >> 16)
}

// walkBoxes iterates top-level boxes in [start,end) of data, calling fn with
// each box type and its body; fn returning false stops the walk.
func walkBoxes(data []byte, start, end int64, fn func(boxType string, body []byte) bool) {
	pos := start
	for pos+8 <= end {
		size := int64(binary.BigEndian.Uint32(data[pos : pos+4]))
		boxType := string(data[pos+4 : pos+8])
		header := int64(8)
		switch size {
		case 0:
			size = end - pos
		case 1:
			if pos+16 > end {
				return
			}
			size = int64(binary.BigEndian.Uint64(data[pos+8 : pos+16]))
			header = 16
		}
		if size < header || pos+size > end {
			return
		}
		bodyStart := pos + header
		if !fn(boxType, data[bodyStart:pos+size]) {
			return
		}
		pos += size
	}
}

// parseMVHD reads timescale/duration from a full-movie header body
// (version 0: 4+4+4+4; version 1: 8+8+4+8). Garbage in -> zeros.
func parseMVHD(body []byte) (timescale, duration uint64) {
	if len(body) < 4 {
		return 0, 0
	}
	version := body[0]
	switch version {
	case 1:
		if len(body) < 28 {
			return 0, 0
		}
		timescale = uint64(binary.BigEndian.Uint32(body[20:24]))
		duration = binary.BigEndian.Uint64(body[24:32])
		return timescale, duration
	default:
		if len(body) < 20 {
			return 0, 0
		}
		timescale = uint64(binary.BigEndian.Uint32(body[12:16]))
		duration = uint64(binary.BigEndian.Uint32(body[16:20]))
		return timescale, duration
	}
}

// parseTKHD reads the track header's display width/height (16.16 fixed).
func parseTKHD(body []byte) (width, height uint32) {
	if len(body) < 4 {
		return 0, 0
	}
	version := body[0]
	var off int
	switch version {
	case 1:
		off = 8 + 8 + 8 + 4 + 4 + 8 // ver/flags + ctime + mtime + trackID + reserved + duration
	default:
		off = 8 + 4 + 4 + 4 + 4 // ver/flags + ctime + mtime + trackID + reserved
	}
	off += 8 // reserved[2]
	off += 2 + 2 + 2 + 2 // layer, alternate_group, volume, reserved
	off += 36 // matrix
	if off+8 > len(body) {
		return 0, 0
	}
	width = binary.BigEndian.Uint32(body[off : off+4])
	height = binary.BigEndian.Uint32(body[off+4 : off+8])
	return width, height
}
