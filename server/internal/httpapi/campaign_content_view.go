package httpapi

import (
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func campaignVersionView(v store.CampaignContentVersion) map[string]any {
	if v.StoreMedia != nil {
		return storeVersionView(*v.StoreMedia)
	}
	return versionView(v.ContentPackVersion)
}

func storeSnapshotRef(m store.StoreMediaSnapshot) *publicMediaRef {
	if m.IsEmpty() {
		return nil
	}
	return &publicMediaRef{MediaID: m.MediaID, SHA256: m.SHA256, MimeType: m.MimeType, ByteSize: m.ByteSize, DurationMS: m.DurationMS, Width: m.Width, Height: m.Height}
}

func storeHandoffMedia(v store.StoreContentPackVersion) []publishhandoff.MediaItem {
	out := []publishhandoff.MediaItem{}
	for _, m := range collectVersionMedia(v) {
		kind := publishhandoff.MediaImage
		if m.role == "video" {
			kind = publishhandoff.MediaVideo
		} else if m.role == "cover" {
			kind = publishhandoff.MediaCover
		}
		out = append(out, publishhandoff.MediaItem{Kind: kind, AssetID: m.mediaID, SHA256: m.sha256, ContentType: m.mime, Filename: m.mediaID + controlledMediaExt(m.mime)})
	}
	return out
}
