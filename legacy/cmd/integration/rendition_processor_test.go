package integration

import (
	"context"
	"errors"

	"websiteengine/features/media"
)

type integrationRenditionProcessor struct {
	failNext bool
}

func (p *integrationRenditionProcessor) Process(_ context.Context, _ media.Media, profile media.FieldProfile, _ []byte) (map[string]media.ProcessedOutput, error) {
	if p.failNext {
		p.failNext = false
		return nil, errors.New("simulated ffmpeg failure")
	}
	outputs := make(map[string]media.ProcessedOutput, len(profile.Renditions)+2)
	if profile.Accept == "video" {
		video := profile.Video
		outputs["video"] = media.ProcessedOutput{Data: []byte("h264"), MIMEType: "video/mp4", Width: video.MaxWidth, Height: video.MaxHeight}
		outputs["poster"] = media.ProcessedOutput{Data: []byte("webp-poster"), MIMEType: "image/webp", Width: video.Poster.Width, Height: video.Poster.Height}
		return outputs, nil
	}
	for _, rendition := range profile.Renditions {
		outputs[rendition.Name] = media.ProcessedOutput{
			Data: []byte("webp-" + rendition.Name), MIMEType: "image/webp",
			Width: rendition.Width, Height: rendition.Height,
		}
	}
	return outputs, nil
}

func (*integrationRenditionProcessor) Ready(context.Context) error { return nil }
