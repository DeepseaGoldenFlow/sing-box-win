package profilefmt

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"
)

// Golden containers emitted by upstream sing-box v1.14.4
// libbox.ProfileContent.Encode. They pin this port: if the port drifts, these
// decode tests fail even though nothing else in the repo imports upstream.
var upstreamGolden = []struct {
	name string
	hex  string
	want Content
}{
	{
		name: "local-utf8",
		hex:  "03011f8b08000000000000ffe2793667cdd3391b5eb6f63edfbb8e818181c1a05a29bfb42429bf342fa558c92aba5aa9a4b22055c94a2925b32835b9444947a924311dc1ad8dad05000000ffff010000ffffa7eca2c742000000",
		want: Content{Name: "本地配置", Type: TypeLocal, Config: `{"outbounds":[{"type":"direct","tag":"direct"}]}`},
	},
	{
		name: "remote-v1",
		hex:  "03011f8b08000000000000ff620e2e4d6260606012a856ca2f2d49ca2fcd4b2956b28a8ead15cd28292928b6d2d74fad48cc2dc849d54bcecfd54f64646060b061606060480dfec800000000ffff010000ffff2edde5113c000000",
		want: Content{Name: "Sub", Type: TypeRemote, Config: `{"outbounds":[]}`, RemotePath: "https://example.com/a", AutoUpdate: true, AutoUpdateInterval: 60, LastUpdated: 1700000000},
	},
}

func TestDecodeUpstreamGolden(t *testing.T) {
	for _, golden := range upstreamGolden {
		t.Run(golden.name, func(t *testing.T) {
			got, err := Decode(unhex(t, golden.hex))
			if err != nil {
				t.Fatal(err)
			}
			if *got != golden.want {
				t.Fatalf("decoded %+v, want %+v", *got, golden.want)
			}
		})
	}
}

func TestEncodeMatchesUpstreamPayloadLayout(t *testing.T) {
	for _, golden := range upstreamGolden {
		t.Run(golden.name, func(t *testing.T) {
			encoded, err := Encode(&golden.want)
			if err != nil {
				t.Fatal(err)
			}
			upstream, err := Decode(unhex(t, golden.hex))
			if err != nil {
				t.Fatal(err)
			}
			// gzip framing bytes are implementation detail; the payload is the contract.
			if got, want := gunzipPayload(t, encoded), gunzipPayload(t, unhex(t, golden.hex)); !bytes.Equal(got, want) {
				t.Fatalf("payload mismatch\n got %x\nwant %x", got, want)
			}
			if *upstream != golden.want {
				t.Fatal("golden drifted")
			}
			roundTrip, err := Decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if *roundTrip != golden.want {
				t.Fatalf("round trip %+v", *roundTrip)
			}
		})
	}
}

func TestDecodeChunkedFraming(t *testing.T) {
	message := unhex(t, upstreamGolden[0].hex)
	chunked := append([]byte{byte(len(message) >> 8), byte(len(message))}, message...)
	got, err := Decode(chunked)
	if err != nil {
		t.Fatal(err)
	}
	if *got != upstreamGolden[0].want {
		t.Fatalf("decoded %+v", *got)
	}
	// A bogus prefix length must not be trusted.
	bad := append([]byte{0xff, 0xff}, message...)
	if _, err = Decode(bad); !errors.Is(err, ErrNotBPF) {
		t.Fatalf("accepted bogus chunk length: %v", err)
	}
	// A container whose message type byte coincides with a plausible chunk length
	// must be read as a container, not as a chunk (byte 3 is the message type here).
	if _, err = Decode(message); err != nil {
		t.Fatalf("plain container misread as chunked: %v", err)
	}
}

func TestLooksLike(t *testing.T) {
	message := unhex(t, upstreamGolden[0].hex)
	chunked := append([]byte{byte(len(message) >> 8), byte(len(message))}, message...)
	for _, data := range [][]byte{message, chunked} {
		if !LooksLike(data) {
			t.Fatalf("container not recognised: %x", data[:min(8, len(data))])
		}
	}
	for _, data := range [][]byte{nil, []byte(`{"a":1}`), []byte{0, 1, 0x1f, 0x8b}, []byte{3, 1, 'x', 'x'}} {
		if LooksLike(data) {
			t.Fatalf("non-container recognised: %x", data)
		}
	}
}

func TestDecodeRejectsWrongInput(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"empty", nil, ErrNotBPF},
		{"json", []byte(`{"outbounds":[]}`), ErrNotBPF},
		{"wrong message type", []byte{0, 1, 0x1f, 0x8b}, ErrNotBPF},
		{"truncated header", []byte{3}, ErrNotBPF},
		{"unsupported version", []byte{3, 2, 0x1f, 0x8b}, ErrUnsupportedVersion},
		{"not gzip", []byte{3, 1, 'x', 'x'}, ErrNotBPF},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Decode(c.data); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestDecodeRejectsCorruptPayload(t *testing.T) {
	encodePayload := func(payload []byte, version byte) []byte {
		var out bytes.Buffer
		out.WriteByte(messageTypeProfileContent)
		out.WriteByte(version)
		zw := gzip.NewWriter(&out)
		if _, err := zw.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	valid := gunzipPayload(t, unhex(t, upstreamGolden[0].hex))
	cases := []struct {
		name    string
		payload []byte
	}{
		{"empty payload", nil},
		{"bogus string length", append([]byte{0xff, 0xff, 0xff, 0xff, 0x0f}, []byte("short")...)},
		{"truncated name", valid[:len(valid)-1]},
		{"unknown profile type", append([]byte{0x01, 'a', 0, 0, 0, 9}, valid[2:]...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Decode(encodePayload(c.payload, 1)); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("got %v, want ErrCorrupt", err)
			}
		})
	}
	// A string length larger than the remainder must fail before allocating it.
	huge := []byte{0x8c, 0x80, 0x80, 0x80, 0x08} // uvarint 1<<33
	if _, err := Decode(encodePayload(huge, 1)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("accepted huge length: %v", err)
	}
}

func TestDecodeRejectsDecompressionBomb(t *testing.T) {
	// Highly compressible payload far beyond the 16 MiB output cap.
	var out bytes.Buffer
	out.WriteByte(messageTypeProfileContent)
	out.WriteByte(1)
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(bytes.Repeat([]byte{0}, MaxSize+4096)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if out.Len() >= MaxSize {
		t.Fatalf("fixture is not compact: %d", out.Len())
	}
	if _, err := Decode(out.Bytes()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bomb not rejected: %v", err)
	}
}

func TestDecodeRejectsOversizedInput(t *testing.T) {
	if _, err := Decode(make([]byte, MaxSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized container not rejected: %v", err)
	}
}

func TestEncodeRejectsInvalidContent(t *testing.T) {
	cases := []struct {
		name    string
		content *Content
	}{
		{"nil", nil},
		{"unknown type", &Content{Name: "x", Type: 9}},
		{"remote without url", &Content{Name: "x", Type: TypeRemote, Config: "{}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Encode(c.content); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("got %v, want ErrCorrupt", err)
			}
		})
	}
	if _, err := Encode(&Content{Name: "本地", Type: TypeLocal, Config: "{}"}); err != nil {
		t.Fatal(err)
	}
}

func unwrapGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	payload, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func gunzipPayload(t *testing.T, container []byte) []byte {
	t.Helper()
	if len(container) < 2 || container[0] != messageTypeProfileContent {
		t.Fatalf("not a container: %x", container)
	}
	return unwrapGzip(t, container[2:])
}

func unhex(t *testing.T, value string) []byte {
	t.Helper()
	if len(value)%2 != 0 {
		t.Fatal("odd hex length")
	}
	out := make([]byte, len(value)/2)
	for i := 0; i < len(out); i++ {
		var b byte
		for j := 0; j < 2; j++ {
			c := value[i*2+j]
			b <<= 4
			switch {
			case c >= '0' && c <= '9':
				b |= c - '0'
			case c >= 'a' && c <= 'f':
				b |= c - 'a' + 10
			default:
				t.Fatalf("bad hex %q", value)
			}
		}
		out[i] = b
	}
	return out
}

func TestReadStringRejectsNonUTF8(t *testing.T) {
	payload := []byte{0x02, 0xff, 0xfe}
	reader := bytes.NewReader(payload)
	if _, err := readString(reader); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v", err)
	}
}
