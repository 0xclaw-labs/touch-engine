package upload

// mediastore_test.go: HUI-3553 受控真实媒体存储的验收矩阵。
//
// 纪律(票面): 上传完成验证实际文件 MIME、长度、哈希;服务器以真实资产元
// 数据决定类型,不接受客户端伪填 MIME;测试用小 mp4/jpeg/png fixture 进
// testdata,生产路径不放假文件。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func expectedSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestMediaStoreSavesRealMP4WithProbedMetadata(t *testing.T) {
	raw := readFixture(t, "tiny.mp4")
	store := NewMediaStore(t.TempDir(), 10<<20)

	meta, err := store.Save(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("save mp4: %v", err)
	}
	if meta.MIME != "video/mp4" {
		t.Fatalf("mime = %q, want video/mp4 (server-detected, not client-claimed)", meta.MIME)
	}
	if meta.Kind != KindVideo {
		t.Fatalf("kind = %q, want video", meta.Kind)
	}
	if meta.SHA256 != expectedSHA(raw) {
		t.Fatalf("sha256 = %s, want %s", meta.SHA256, expectedSHA(raw))
	}
	if meta.ByteSize != int64(len(raw)) {
		t.Fatalf("byte_size = %d, want %d", meta.ByteSize, len(raw))
	}
	if meta.DurationMS < 900 || meta.DurationMS > 1500 {
		t.Fatalf("duration_ms = %d, want ~1000 (real probe)", meta.DurationMS)
	}
	if meta.Width != 64 || meta.Height != 64 {
		t.Fatalf("dimensions = %dx%d, want 64x64 (real probe)", meta.Width, meta.Height)
	}

	// saved bytes are readable back and identical (受控存储真实文件)
	f, err := store.Open(meta.SHA256, ".mp4")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	got := make([]byte, len(raw))
	if _, err := f.Read(got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("stored bytes differ from upload")
	}
}

func TestMediaStoreSavesImages(t *testing.T) {
	jpg := readFixture(t, "pixel.jpg")
	png := readFixture(t, "pixel.png")
	store := NewMediaStore(t.TempDir(), 10<<20)

	mj, err := store.Save(bytes.NewReader(jpg))
	if err != nil {
		t.Fatalf("save jpg: %v", err)
	}
	if mj.MIME != "image/jpeg" || mj.Kind != KindImage {
		t.Fatalf("jpg = %s/%s, want image/jpeg|image", mj.MIME, mj.Kind)
	}
	mp, err := store.Save(bytes.NewReader(png))
	if err != nil {
		t.Fatalf("save png: %v", err)
	}
	if mp.MIME != "image/png" || mp.Kind != KindImage {
		t.Fatalf("png = %s/%s, want image/png|image", mp.MIME, mp.Kind)
	}
}

func TestMediaStoreRejectsUnknownBytes(t *testing.T) {
	store := NewMediaStore(t.TempDir(), 10<<20)
	if _, err := store.Save(strings.NewReader("this is not media at all, just text")); err == nil {
		t.Fatal("expected rejection of non-media bytes (客户端伪 MIME 不采信)")
	}
}

func TestMediaStoreEnforcesSizeCap(t *testing.T) {
	store := NewMediaStore(t.TempDir(), 8)
	// 64 bytes of plausible mp4-ish content but larger than the cap
	big := bytes.Repeat([]byte("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2"), 4)
	if _, err := store.Save(bytes.NewReader(big)); err == nil {
		t.Fatal("expected size-cap rejection")
	}
}

func TestMediaStoreDeduplicatesSameContent(t *testing.T) {
	raw := readFixture(t, "pixel.png")
	store := NewMediaStore(t.TempDir(), 10<<20)
	first, err := store.Save(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("save 1: %v", err)
	}
	second, err := store.Save(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if first.SHA256 != second.SHA256 || first.MediaRef != second.MediaRef {
		t.Fatalf("dedupe failed: %+v vs %+v", first, second)
	}
	if !second.Replayed {
		t.Fatal("second save should report replayed=true")
	}
}

func TestMediaStorePartialMP4BestEffort(t *testing.T) {
	// ftyp-only mp4 (no moov box): MIME/kind still real from magic bytes,
	// probe leaves duration/dimensions at 0 rather than inventing them.
	raw := append([]byte{0x00, 0x00, 0x00, 0x14, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0x00, 0x00, 0x02, 0x00, 'i', 's', 'o', 'm'}, make([]byte, 32)...)
	store := NewMediaStore(t.TempDir(), 10<<20)
	meta, err := store.Save(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if meta.MIME != "video/mp4" || meta.Kind != KindVideo {
		t.Fatalf("meta = %s/%s, want video/mp4", meta.MIME, meta.Kind)
	}
	if meta.DurationMS != 0 || meta.Width != 0 || meta.Height != 0 {
		t.Fatalf("probe must not invent metadata: %+v", meta)
	}
}

func TestMediaStoreOpenMissing(t *testing.T) {
	store := NewMediaStore(t.TempDir(), 10<<20)
	if _, err := store.Open("deadbeef", ".mp4"); err == nil {
		t.Fatal("expected ErrMediaNotFound for absent media")
	}
}
