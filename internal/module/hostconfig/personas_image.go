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

// webpSize reads the canvas size from a WebP: RIFF....WEBP whose RIFF size is the file's, and a first chunk of VP8 (lossy),
// VP8L (lossless) or VP8X (extended) that fits in the file and is big enough for its header. An extended header must be
// followed by a chunk that carries the picture (still VP8 or VP8L, alpha, or an animation). There is no WebP decoder in the
// daemon, so the picture data itself is not read: this refuses the forged and the cut off, not every broken file.
func webpSize(b []byte) (int, int, error) {
	bad := errors.New("avatar_data is not a WebP picture")
	if len(b) < 20 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" || int(binary.LittleEndian.Uint32(b[4:8])) != len(b)-8 {
		return 0, 0, bad
	}
	size := int(binary.LittleEndian.Uint32(b[16:20]))
	end := 20 + size + size%2
	if size < 0 || end > len(b) {
		return 0, 0, bad
	}
	switch string(b[12:16]) {
	case "VP8 ":
		if size < 10 || b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, bad
		}
		return int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff), nil
	case "VP8L":
		if size < 5 || b[20] != 0x2f {
			return 0, 0, bad
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	case "VP8X":
		if size < 10 || !webpHasPicture(b, end) {
			return 0, 0, bad
		}
		w := int(b[24]) | int(b[25])<<8 | int(b[26])<<16
		h := int(b[27]) | int(b[28])<<8 | int(b[29])<<16
		return w + 1, h + 1, nil
	}
	return 0, 0, bad
}

// webpHasPicture walks the chunks of an extended WebP from `at` to the end of the file, every one inside it: the file must
// hold a still picture (a VP8 or VP8L chunk, alpha and the metadata chunks allowed before it) or an animation (ANIM, then a
// frame ANMF big enough for its own header). Any other chunk makes the container unreadable for the App.
func webpHasPicture(b []byte, at int) bool {
	animated := false
	for at+8 <= len(b) {
		n := int(binary.LittleEndian.Uint32(b[at+4 : at+8]))
		next := at + 8 + n + n%2
		if n < 0 || next > len(b) {
			return false
		}
		switch string(b[at : at+4]) {
		case "VP8 ":
			return n >= 10
		case "VP8L":
			return n >= 5
		case "ANIM":
			animated = n >= 6
		case "ANMF":
			return animated && n >= 16
		case "ALPH", "ICCP", "EXIF", "XMP ":
		default:
			return false
		}
		at = next
	}
	return false
}
