package hostconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	_ "image/jpeg" // registered for image.DecodeConfig
	_ "image/png"
	"strings"
)

const (
	// maxAvatarDataBytes bounds the avatar_data string (spec §4.1).
	maxAvatarDataBytes = 32 * 1024
	// maxAvatarSide is the largest width and height of a stored picture.
	maxAvatarSide = 128
)

// checkAvatarData validates a persona's picture (spec §4.1): "" (the App draws initials), or a data URI of a WebP, PNG or
// JPEG that decodes as base64, whose bytes carry that format's signature, and whose size is at most 128×128. SVG and every
// other type are refused (a script surface). Only the header is read for the size.
func checkAvatarData(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > maxAvatarDataBytes {
		return errors.New("avatar_data is over 32 KB")
	}
	head, payload, ok := strings.Cut(s, ",")
	if !ok || !strings.HasSuffix(head, ";base64") || !strings.HasPrefix(head, "data:image/") {
		return errors.New("avatar_data must be a data:image/(webp|png|jpeg);base64, URI")
	}
	mime := strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return errors.New("avatar_data is not valid base64")
	}
	var w, h int
	switch mime {
	case "image/png":
		if !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
			return errors.New("avatar_data is not a PNG")
		}
		w, h, err = decodeConfig(raw)
	case "image/jpeg":
		if !bytes.HasPrefix(raw, []byte{0xFF, 0xD8, 0xFF}) {
			return errors.New("avatar_data is not a JPEG")
		}
		w, h, err = decodeConfig(raw)
	case "image/webp":
		w, h, err = webpSize(raw)
	default:
		return errors.New("avatar_data must be a webp, png or jpeg picture")
	}
	if err != nil {
		return err
	}
	if w < 1 || h < 1 || w > maxAvatarSide || h > maxAvatarSide {
		return errors.New("avatar_data must be at most 128×128 pixels")
	}
	return nil
}

func decodeConfig(raw []byte) (int, int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0, errors.New("avatar_data is not a readable picture")
	}
	return cfg.Width, cfg.Height, nil
}

// webpSize reads the canvas size from a WebP header: RIFF....WEBP and a first chunk of VP8 (lossy), VP8L (lossless) or VP8X
// (extended).
func webpSize(b []byte) (int, int, error) {
	bad := errors.New("avatar_data is not a WebP picture")
	if len(b) < 25 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, bad
	}
	switch string(b[12:16]) {
	case "VP8 ":
		if len(b) < 30 || b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, bad
		}
		return int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff), nil
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, bad
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	case "VP8X":
		if len(b) < 30 {
			return 0, 0, bad
		}
		w := int(b[24]) | int(b[25])<<8 | int(b[26])<<16
		h := int(b[27]) | int(b[28])<<8 | int(b[29])<<16
		return w + 1, h + 1, nil
	}
	return 0, 0, bad
}
