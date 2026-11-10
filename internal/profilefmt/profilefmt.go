// Package profilefmt implements the sing-box BPF profile container used by SFM
// and other official clients.
//
// The wire format is not invented here. It is a byte-for-byte port of the
// upstream implementation in sing-box v1.14.4:
//
//	experimental/libbox/profile_import.go:166-253  ProfileContent encode/decode
//	experimental/libbox/profile_import.go:255-280  uvarint-prefixed strings
//
// It is ported instead of imported because importing experimental/libbox links
// internal/oomprofile, whose //go:linkname references are rejected by Go 1.26
// on linux/amd64. That would break `go test ./...`, which is the acceptance
// command run on the Linux side. profilefmt_test.go pins the port against
// golden vectors emitted by upstream libbox, so any wire drift fails the tests.
//
// Differences from upstream are limited to hardening, never format:
//   - every length is validated against the remaining buffer before use;
//   - decompression is capped, so a gzip bomb cannot exhaust memory;
//   - unsupported message types and dead versions are rejected with a typed error.
package profilefmt

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// MaxSize bounds both the compressed container and its decompressed payload.
// The rest of the client already caps configurations at 16 MiB.
const MaxSize = 16 << 20

// Message type and profile type values mirror upstream libbox constants.
const (
	messageTypeProfileContent = 3 // libbox.MessageTypeProfileContent

	TypeLocal  int32 = 0 // libbox.ProfileTypeLocal
	TypeICloud int32 = 1 // libbox.ProfileTypeiCloud
	TypeRemote int32 = 2 // libbox.ProfileTypeRemote

	// currentVersion is the only version this client writes and the newest it
	// accepts. Upstream also understands version 0 (profiles without an update
	// interval); both are read here.
	currentVersion = 1
)

var (
	// ErrNotBPF reports a file that is not a sing-box profile container.
	ErrNotBPF = errors.New("不是有效的 BPF 配置")
	// ErrTooLarge reports a container or its payload exceeding MaxSize.
	ErrTooLarge = errors.New("BPF 配置超过 16 MiB 上限")
	// ErrCorrupt reports truncated or internally inconsistent data.
	ErrCorrupt = errors.New("BPF 配置数据损坏")
	// ErrUnsupportedVersion reports a container newer than this client knows.
	ErrUnsupportedVersion = errors.New("不支持的 BPF 配置版本")
)

// Content is the profile carried by a BPF container.
type Content struct {
	Name               string
	Type               int32
	Config             string
	RemotePath         string
	AutoUpdate         bool
	AutoUpdateInterval int32
	LastUpdated        int64
}

// IsRemote reports whether the profile is backed by a remote URL.
func (c *Content) IsRemote() bool { return c.Type == TypeRemote }

// Encode serialises c exactly as libbox.ProfileContent.Encode does.
//
// Byte layout:
//
//	message type (1 byte, 3) | version (1 byte, 1) | gzip payload
//
// gzip payload:
//
//	uvarint len + name
//	int32 BE type
//	uvarint len + config
//	[type != local] uvarint len + remote path
//	[type == remote] bool (1 byte) + int32 BE update minutes + int64 BE unix updated
func Encode(c *Content) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: 配置为空", ErrCorrupt)
	}
	if c.Type < TypeLocal || c.Type > TypeRemote {
		return nil, fmt.Errorf("%w: 未知配置类型 %d", ErrCorrupt, c.Type)
	}
	if c.Type == TypeRemote && strings.TrimSpace(c.RemotePath) == "" {
		return nil, fmt.Errorf("%w: 远程配置缺少订阅地址", ErrCorrupt)
	}
	var payload bytes.Buffer
	writeString(&payload, c.Name)
	_ = binary.Write(&payload, binary.BigEndian, c.Type)
	writeString(&payload, c.Config)
	if c.Type != TypeLocal {
		writeString(&payload, c.RemotePath)
	}
	if c.Type == TypeRemote {
		var flag byte
		if c.AutoUpdate {
			flag = 1
		}
		payload.WriteByte(flag)
		_ = binary.Write(&payload, binary.BigEndian, c.AutoUpdateInterval)
		_ = binary.Write(&payload, binary.BigEndian, c.LastUpdated)
	}
	if payload.Len() > MaxSize {
		return nil, ErrTooLarge
	}
	var out bytes.Buffer
	out.WriteByte(messageTypeProfileContent)
	out.WriteByte(currentVersion)
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(payload.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if out.Len() > MaxSize {
		return nil, ErrTooLarge
	}
	return out.Bytes(), nil
}

// Decode parses a BPF container. A 2-byte big-endian length prefix (the chunk
// framing produced by libbox.EncodeChunkedMessage) is accepted as well, because
// some clients write that framing to files.
func Decode(data []byte) (*Content, error) {
	if len(data) == 0 {
		return nil, ErrNotBPF
	}
	if len(data) > MaxSize {
		return nil, ErrTooLarge
	}
	// Length-prefixed chunk: uint16 BE message length followed by the message
	// (libbox.EncodeChunkedMessage). Only an exact, self-consistent prefix is
	// accepted so a corrupt file is not silently re-interpreted. The gzip magic
	// check keeps a plain container whose first payload byte happens to be 3 from
	// being mistaken for a chunk.
	if !hasProfileHeader(data) && len(data) >= 5 {
		length := int(binary.BigEndian.Uint16(data[:2]))
		if length == len(data)-2 && hasProfileHeader(data[2:]) {
			data = data[2:]
		}
	}
	if !hasProfileHeader(data) {
		return nil, ErrNotBPF
	}
	version := data[1]
	if version > currentVersion {
		return nil, fmt.Errorf("%w: version %d", ErrUnsupportedVersion, version)
	}
	payload, err := gunzipBounded(data[2:])
	if err != nil {
		return nil, err
	}
	return decodePayload(payload, version)
}

// LooksLike reports whether data could be a profile container, so callers that
// sniff file content (an extension is only a hint) can choose a decoder. It
// checks the header and the optional chunk framing only; Decode does the real
// validation.
func LooksLike(data []byte) bool {
	if hasProfileHeader(data) {
		return true
	}
	if len(data) >= 5 {
		length := int(binary.BigEndian.Uint16(data[:2]))
		return length == len(data)-2 && hasProfileHeader(data[2:])
	}
	return false
}

// hasProfileHeader reports whether data starts with a profile message header:
// message type 3, then a version byte, then the gzip magic. Requiring the gzip
// bytes is what distinguishes a real container from a chunk length prefix. The
// version itself is checked later so an unsupported version keeps its own error.
func hasProfileHeader(data []byte) bool {
	return len(data) >= 4 && data[0] == messageTypeProfileContent && data[2] == 0x1f && data[3] == 0x8b
}

// gunzipBounded decompresses at most MaxSize+1 bytes so a bomb is detected
// without allocating the whole expansion.
func gunzipBounded(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotBPF, err)
	}
	defer zr.Close()
	payload, err := io.ReadAll(io.LimitReader(zr, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if len(payload) > MaxSize {
		return nil, ErrTooLarge
	}
	return payload, nil
}

func decodePayload(payload []byte, version byte) (*Content, error) {
	reader := bytes.NewReader(payload)
	var content Content
	var err error
	if content.Name, err = readString(reader); err != nil {
		return nil, err
	}
	if err = binary.Read(reader, binary.BigEndian, &content.Type); err != nil {
		return nil, fmt.Errorf("%w: 缺少配置类型", ErrCorrupt)
	}
	if content.Type < TypeLocal || content.Type > TypeRemote {
		return nil, fmt.Errorf("%w: 未知配置类型 %d", ErrCorrupt, content.Type)
	}
	if content.Config, err = readString(reader); err != nil {
		return nil, err
	}
	if content.Type != TypeLocal {
		if content.RemotePath, err = readString(reader); err != nil {
			return nil, err
		}
	}
	if content.Type == TypeRemote || (version == 0 && content.Type != TypeLocal) {
		var autoUpdate bool
		if err = binary.Read(reader, binary.BigEndian, &autoUpdate); err != nil {
			return nil, fmt.Errorf("%w: 缺少自动更新标记", ErrCorrupt)
		}
		content.AutoUpdate = autoUpdate
		if version >= 1 {
			if err = binary.Read(reader, binary.BigEndian, &content.AutoUpdateInterval); err != nil {
				return nil, fmt.Errorf("%w: 缺少自动更新间隔", ErrCorrupt)
			}
			if content.AutoUpdateInterval < 0 {
				return nil, fmt.Errorf("%w: 自动更新间隔无效", ErrCorrupt)
			}
		}
		if err = binary.Read(reader, binary.BigEndian, &content.LastUpdated); err != nil {
			return nil, fmt.Errorf("%w: 缺少更新时间", ErrCorrupt)
		}
	}
	if content.Type == TypeRemote && strings.TrimSpace(content.RemotePath) == "" {
		return nil, fmt.Errorf("%w: 远程配置缺少订阅地址", ErrCorrupt)
	}
	return &content, nil
}

// readString mirrors libbox.readString, with the length checked against the
// bytes actually remaining so a bogus uvarint cannot trigger a huge allocation.
func readString(reader *bytes.Reader) (string, error) {
	length, err := binary.ReadUvarint(reader)
	if err != nil {
		return "", fmt.Errorf("%w: 字符串长度无效", ErrCorrupt)
	}
	if length > uint64(reader.Len()) {
		return "", fmt.Errorf("%w: 字符串长度超出数据范围", ErrCorrupt)
	}
	if length == 0 {
		return "", nil
	}
	value := make([]byte, length)
	if _, err = io.ReadFull(reader, value); err != nil {
		return "", fmt.Errorf("%w: 字符串被截断", ErrCorrupt)
	}
	if !utf8.Valid(value) {
		return "", fmt.Errorf("%w: 字符串不是有效 UTF-8", ErrCorrupt)
	}
	return string(value), nil
}

// writeString mirrors libbox.writeString: uvarint byte length, then raw bytes.
func writeString(buffer *bytes.Buffer, value string) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], uint64(len(value)))
	buffer.Write(scratch[:n])
	buffer.WriteString(value)
}
