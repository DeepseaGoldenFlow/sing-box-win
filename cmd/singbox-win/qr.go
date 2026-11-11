package main

// Configuration QR sharing.
//
// The payload is not an SFW-private format:
//   - the primary QR carries the standard sing-box ProfileContent container
//     produced by internal/profilefmt (port of
//     experimental/libbox/profile_import.go:166-253), so the bytes are the same
//     ones a .bpf export produces;
//   - when that container does not fit one symbol, the QR is downgraded to the
//     standard subscription link built exactly like
//     experimental/libbox/remote_profile.go:7-15.
//
// Official clients read a scanned QR as text (SFM: QRScannerView +
// LibboxParseRemoteProfileImportLink). This client therefore imports a raw
// ProfileContent from the QR byte segment first and only falls back to the
// decoded text, because a container's gzip bytes are not valid UTF-8 and a
// text-only read would corrupt them. A single-frame binary QR is readable by
// SFW itself; SFM's multi-frame binary sharing is QRS, which is out of scope
// here (see docs/sfm-feature-spec.md:97,111).

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // let users import a phone screenshot as well as a PNG
	_ "image/png"
	"strings"

	"github.com/makiuchi-d/gozxing"
	zxingqr "github.com/makiuchi-d/gozxing/qrcode"
	"github.com/skip2/go-qrcode"

	"github.com/JARVIS/sing-box-win/internal/profilefmt"
)

const (
	// qrMaxBytes is the payload ceiling of a single QR symbol: version 40 at
	// error-correction level L in byte mode holds 2953 bytes (ISO/IEC 18004
	// capacity table; github.com/skip2/go-qrcode quotes the same figure, and
	// TestQRCapacityConstantMatchesEncoder pins the encoder to it).
	//
	// Level L is deliberate: it maximises payload for a screen-shown or
	// losslessly saved PNG. Level M would only carry 2331 bytes, pushing many
	// real subscriptions into the deep-link fallback for no benefit.
	qrMaxBytes = 2953

	// qrImageScale is the PNG module size in pixels (negative means
	// "per-module" in skip2/go-qrcode). 6 px per module keeps a version-40
	// symbol below ~1500 px while staying comfortably decodable.
	qrImageScale = -6

	// qrImageLimit bounds the image file an import may read. A phone
	// screenshot is normally well under this; the limit exists so a crafted
	// upload cannot exhaust memory before decoding starts.
	qrImageLimit = 8 << 20

	// qrImagePixels rejects decompression bombs: a small PNG can declare huge
	// dimensions, and image.Decode would then allocate them.
	qrImagePixels = 24_000_000

	// qrFallbackNote explains an automatic downgrade in the UI.
	qrFallbackNote = "配置超过单张二维码容量，已降级为订阅链接二维码"
)

var (
	// errQRTooLarge is shown verbatim to the user; the wording is part of the
	// task contract (CMP-40 §2).
	errQRTooLarge = errors.New("此配置过大，无法用二维码分享，请改用导出文件")
	// errQRNoContent reports a decodable image that holds no importable value.
	errQRNoContent = errors.New("二维码内容不是 sing-box 配置或导入链接")
)

// qrShare is a QR payload ready to be rendered.
//
// Exactly one of Payload (raw ProfileContent bytes) and Text (standard deep
// link) is set, selected by Format.
type qrShare struct {
	Format  string // "bpf" or "deep-link"
	Payload []byte // Format == "bpf"
	Text    string // Format == "deep-link"
	Note    string // optional explanation of an automatic downgrade
	Size    int    // bytes carried by the symbol
}

// profileQR builds the QR payload for p.
//
// A container that fits is shared as-is. One that does not is never truncated:
// remote profiles fall back to their subscription deep link, and local profiles
// (or remote ones without a usable URL) fail with errQRTooLarge.
func (a *App) profileQR(p Profile) (qrShare, error) {
	container, _, err := a.exportProfile(p, "bpf")
	if err != nil {
		return qrShare{}, err
	}
	if len(container) <= qrMaxBytes {
		return qrShare{Format: "bpf", Payload: container, Size: len(container)}, nil
	}
	if p.Type != "remote" || !validProfileURL(p.URL) {
		return qrShare{}, errQRTooLarge
	}
	link := generateFmtDeepLink(p.Name, p.URL)
	// A pathological subscription URL can itself exceed one symbol; falling
	// back again would silently produce an unscannable image.
	if len(link) > qrMaxBytes {
		return qrShare{}, errQRTooLarge
	}
	return qrShare{Format: "deep-link", Text: link, Note: qrFallbackNote, Size: len(link)}, nil
}

// qrPNG renders payload as a PNG QR image.
func qrPNG(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("二维码内容为空")
	}
	if len(payload) > qrMaxBytes {
		return nil, errQRTooLarge
	}
	png, err := qrcode.Encode(string(payload), qrcode.Low, qrImageScale)
	if err != nil {
		return nil, fmt.Errorf("生成二维码失败：%v", err)
	}
	return png, nil
}

// qrContent is what a scanned image yielded.
type qrContent struct {
	BPF  []byte // a ProfileContent container
	Link string // a sing-box/singboxwin import link
}

// decodeQRPNG decodes a QR image and classifies its content.
//
// The raw byte segment is preferred over Result.GetText(): gozxing decodes byte
// segments through a charset guess, so GetText() mangles non-UTF-8 binary such
// as a ProfileContent container. The segment bytes themselves are exact.
func decodeQRPNG(data []byte) (qrContent, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return qrContent{}, errors.New("二维码图片为空")
	}
	if len(data) > qrImageLimit {
		return qrContent{}, errors.New("二维码图片超过 8 MiB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return qrContent{}, fmt.Errorf("无法读取二维码图片：%v", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return qrContent{}, errors.New("无法读取二维码图片：尺寸无效")
	}
	if config.Width*config.Height > qrImagePixels {
		return qrContent{}, errors.New("二维码图片尺寸过大")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// DecodeConfig succeeded but the pixels did not: truncated upload.
		return qrContent{}, fmt.Errorf("二维码图片已损坏：%v", err)
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return qrContent{}, fmt.Errorf("无法读取二维码图片：%v", err)
	}
	result, err := zxingqr.NewQRCodeReader().Decode(bitmap, map[gozxing.DecodeHintType]interface{}{
		gozxing.DecodeHintType_TRY_HARDER: true,
	})
	if err != nil {
		return qrContent{}, fmt.Errorf("无法识别二维码图片中的内容：%v", err)
	}
	// QRS-style multi-symbol sharing is not implemented; a fragment on its own
	// is not a profile and must not be guessed at.
	if _, ok := result.GetResultMetadata()[gozxing.ResultMetadataType_STRUCTURED_APPEND_SEQUENCE]; ok {
		return qrContent{}, errors.New("不支持分片二维码（QRS），请改用单张二维码或配置文件")
	}
	segments, _ := result.GetResultMetadata()[gozxing.ResultMetadataType_BYTE_SEGMENTS].([][]byte)
	if len(segments) > 0 {
		var joined bytes.Buffer
		for _, segment := range segments {
			joined.Write(segment)
		}
		payload := joined.Bytes()
		if profilefmt.LooksLike(payload) {
			// gozxing exposes only byte-mode segments. If the encoder split the
			// data into numeric/alphanumeric segments too, payload is truncated;
			// a full decode is what proves we recovered the whole container.
			if _, err := profilefmt.Decode(payload); err != nil {
				return qrContent{}, fmt.Errorf("二维码中的配置不完整或已损坏，请重新生成二维码图片：%v", err)
			}
			return qrContent{BPF: payload}, nil
		}
	}
	// Links are ordinary text and always decode exactly, so they are checked
	// after the container path (a URL with lower-case letters also produces a
	// byte segment, and that segment alone is not a container).
	text := strings.TrimSpace(result.GetText())
	if isDeepLinkArg(text) {
		return qrContent{Link: text}, nil
	}
	return qrContent{}, errQRNoContent
}

// importQRImage decodes a QR image and imports what it carries through the
// existing validation paths: BPF bytes go to importProfileBytes (size cap,
// decompression cap, unique name) and links go to importDeepLink (URL check,
// download cap, unique name). name overrides the profile name when non-empty.
func (a *App) importQRImage(data []byte, name string) (Profile, error) {
	content, err := decodeQRPNG(data)
	if err != nil {
		return Profile{}, err
	}
	if len(content.BPF) > 0 {
		return a.importProfileBytes(content.BPF, name)
	}
	return a.importDeepLinkAs(content.Link, name)
}
