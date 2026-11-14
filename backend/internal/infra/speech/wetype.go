package speech

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"github.com/google/uuid"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const wetypeSignKey = "zN7rB3bL4pO8jW1o"
const wetypeBootKey = "D4Y5U3Y2M0C0T7N4P1P7O2N6E1I2Y1U6"
const wetypeDH uint64 = 2147483646
const wetypeUIN uint64 = 0x7ffffdfd

type WeType struct {
	cfg IMEConfig
	mu  sync.Mutex
}

func NewWeType(cfg IMEConfig) *WeType {
	if cfg.WSURL == "" {
		cfg.WSURL = "wss://wetype.weixin.qq.com/"
	}
	return &WeType{cfg: cfg}
}
func (w *WeType) ASRCapability() *domain.Capability {
	return &domain.Capability{ID: "wetype", Name: "微信输入法", Streaming: true, Formats: []string{"opus"}, SampleRate: 16000}
}
func (w *WeType) TTSCapability() *domain.Capability { return nil }
func (w *WeType) Transcribe(context.Context, domain.Audio) (domain.Transcript, error) {
	return domain.Transcript{}, domain.ErrUnavailable
}
func (w *WeType) Synthesize(context.Context, domain.Synthesis) (io.ReadCloser, string, error) {
	return nil, "", domain.ErrUnavailable
}

type wetypeIdentity struct {
	Device string `json:"device"`
	UIN    string `json:"uin"`
}
type wetypeClient struct {
	ws            *websocket.Conn
	identity      wetypeIdentity
	key           []byte
	server, token string
	task          uint64
}
type wetypeReply struct {
	status  uint64
	headers map[string]string
	body    []byte
}

func upperMD5(b []byte) string { h := md5.Sum(b); return strings.ToUpper(hex.EncodeToString(h[:])) }
func (c *wetypeClient) request(path string, body []byte, cmd uint64, compression, token string) (wetypeReply, error) {
	special := cmd == wetypeDH || cmd == wetypeUIN
	task := cmd
	if !special {
		c.task++
		task = c.task
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	trace := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	checksum := upperMD5(body)
	cmdS := strconv.FormatUint(cmd, 10)
	taskS := strconv.FormatUint(task, 10)
	parts := []string{"5", "2.2.3(657)", "2"}
	if cmd == wetypeDH {
		parts = append(parts, ts, checksum, trace, cmdS)
	} else if cmd == wetypeUIN {
		parts = append(parts, ts, checksum, token, trace, cmdS)
	} else {
		parts = append(parts, cmdS, "0", ts, checksum, c.identity.UIN, trace, taskS)
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "") + wetypeSignKey))
	headers := map[string]string{"Kb-Uin": c.identity.UIN, "Kb-OsType": "5", "Kb-Version": "2.2.3(657)", "Kb-SystemVersion": "27.0.0", "Kb-PackageType": "3", "Use_DebugNet": "0", "Kb-TimeStamp": ts, "Kb-BodyMd5": checksum, "Kb-TraceId": trace, "Kb-TaskId": taskS, "Kb-Scene": "2", "Kb-Sign": strings.ToUpper(hex.EncodeToString(h[:])), "Content-Length": strconv.Itoa(len(body)), "Kb-CompressionType": compression, "Content-Type": "application/octet-stream", "HOST": "wetype.weixin.qq.com"}
	if token != "" {
		headers["Kb-GenUinToken"] = token
	}
	if special {
		headers["Kb-DeviceCodeRestrictionV2"] = "1"
	}
	if cmd != wetypeDH {
		headers["Kb-CmdId"] = cmdS
		headers["Kb-SubCmdId"] = "0"
		if len(c.key) >= 4 {
			headers["Kb-SharedKeySuffix"] = string(c.key[len(c.key)-4:])
		}
	}
	request := proto{}.s(1, "POST").s(3, path).s(4, "")
	for k, v := range headers {
		request = request.b(5, proto{}.s(1, k).s(2, v))
	}
	request = request.b(6, body)
	frame := proto{}.v(1, 0).v(2, 0).v(3, task).b(5, request)
	if e := sendIME(c.ws, frame); e != nil {
		return wetypeReply{}, e
	}
	raw, e := recvIME(c.ws, 15*time.Second)
	if e != nil {
		return wetypeReply{}, e
	}
	top, e := parseProto(raw)
	if e != nil {
		return wetypeReply{}, e
	}
	f, e := parseProto(data(top, 4))
	if e != nil {
		return wetypeReply{}, e
	}
	r := wetypeReply{status: integer(f, 2), body: data(f, 5), headers: map[string]string{}}
	for _, v := range f {
		if v.number == 5 || v.data == nil {
			continue
		}
		pair, e := parseProto(v.data)
		if e == nil && len(pair) >= 2 {
			r.headers[strings.ToLower(string(pair[0].data))] = string(pair[1].data)
		}
	}
	return r, nil
}
func (c *wetypeClient) exchange(newIdentity bool) error {
	private, public, e := weTypeKeypair()
	if e != nil {
		return e
	}
	p := proto{}.s(1, public).s(2, public)
	if newIdentity {
		p = p.v(3, 1)
	}
	p = p.s(4, c.identity.Device)
	if !newIdentity {
		n, _ := strconv.ParseUint(c.identity.UIN, 10, 64)
		p = p.v(5, n)
	}
	p = p.s(7, "").s(8, "Mac16,12").s(9, "")
	encrypted, e := aesEncrypt([]byte(wetypeBootKey), p)
	if e != nil {
		return e
	}
	r, e := c.request("/oauth_pubkey_v2", encrypted, wetypeDH, "1", "")
	if e != nil || r.status != 200 {
		return domain.ErrUnavailable
	}
	plain, e := aesDecrypt([]byte(wetypeBootKey), r.body)
	if e != nil {
		return e
	}
	f, e := parseProto(plain)
	if e != nil {
		return e
	}
	c.server = string(data(f, 2))
	c.token = string(data(f, 4))
	if newIdentity && c.token == "" {
		return domain.ErrForbidden
	}
	c.key, e = weTypeShared(private, c.server)
	return e
}
func (w *WeType) open(ctx context.Context, user uint) (*wetypeClient, func(), error) {
	ws, closeWS, e := dialIME(ctx, w.cfg.WSURL, http.Header{}, "wxws_pb")
	if e != nil {
		return nil, nil, e
	}
	c := &wetypeClient{ws: ws, identity: wetypeIdentity{UIN: "0"}}
	ok := false
	defer func() {
		if !ok {
			closeWS()
		}
	}()
	if _, e = c.request("/timestamp", nil, 0, "1", ""); e != nil {
		return nil, nil, e
	}
	if locks, ok := w.cfg.Identities.(interface{ LockIdentity(uint, string) func() }); ok {
		unlock := locks.LockIdentity(user, "wetype")
		defer unlock()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cfg.Identities == nil {
		return nil, nil, domain.ErrUnavailable
	}
	saved, e := w.cfg.Identities.LoadIdentity(ctx, user, "wetype")
	existing := e == nil
	if existing {
		if json.Unmarshal(saved, &c.identity) != nil || c.identity.Device == "" || c.identity.UIN == "" {
			return nil, nil, domain.ErrInvalid
		}
		if e = c.exchange(false); e != nil {
			return nil, nil, e
		}
	} else {
		if !os.IsNotExist(e) {
			return nil, nil, e
		}
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		body := "MAC" + strings.Repeat("0", 17-len("Mac16,12")) + "Mac16,12" + suffix
		c.identity.Device = body + upperMD5([]byte(body+wetypeSignKey))
		if e = c.exchange(true); e != nil {
			return nil, nil, e
		}
		p := proto{}.s(1, c.identity.Device).s(2, c.server).s(4, c.token)
		encrypted, _ := aesEncrypt(c.key, p)
		r, e := c.request("/gen_uin_v2", encrypted, wetypeUIN, "1", c.token)
		if e != nil || r.status != 200 {
			return nil, nil, domain.ErrForbidden
		}
		plain, e := aesDecrypt(c.key, r.body)
		if e != nil {
			return nil, nil, e
		}
		f, e := parseProto(plain)
		if e != nil {
			return nil, nil, e
		}
		uin := integer(f, 2)
		if uin == 0 {
			return nil, nil, domain.ErrForbidden
		}
		c.identity.UIN = strconv.FormatUint(uin, 10)
		b, _ := json.Marshal(c.identity)
		if e = w.cfg.Identities.SaveIdentity(ctx, user, "wetype", b); e != nil {
			return nil, nil, e
		}
	}
	encrypted, _ := aesEncrypt(c.key, nil)
	r, e := c.request("/api_v2", encrypted, 8074, "1", "")
	if e != nil || r.status != 200 {
		return nil, nil, domain.ErrUnavailable
	}
	ok = true
	return c, closeWS, nil
}
func (c *wetypeClient) voice(id string, audio []byte, seq, total uint64, end bool) (string, string, error) {
	p := proto{}.s(2, id)
	if len(audio) > 0 {
		p = p.b(4, audio)
	}
	p = p.v(5, 5)
	if end {
		p = p.v(6, 1)
	}
	p = p.v(7, seq)
	if total > 0 {
		p = p.v(11, total)
	}
	p = p.v(22, 1).v(23, 1).v(24, 1)
	body, e := aesEncrypt(c.key, snappyEncode(proto{}.b(1, p)))
	if e != nil {
		return "", "", e
	}
	r, e := c.request("/api_v2", body, 4548, "2", "")
	if e != nil {
		return "", "", e
	}
	if r.status != 200 {
		return "", "", domain.ErrUnavailable
	}
	plain, e := aesDecrypt(c.key, r.body)
	if e != nil {
		return "", "", e
	}
	if r.headers["kb-compressiontype"] == "2" || r.headers["compressiontype"] == "2" {
		plain, e = snappyDecode(plain)
		if e != nil {
			return "", "", e
		}
	}
	f, e := parseProto(plain)
	if e != nil {
		return "", "", e
	}
	if inner := data(f, 1); len(inner) > 0 {
		f, e = parseProto(inner)
		if e != nil {
			return "", "", e
		}
	}
	return string(data(f, 4)), string(data(f, 14)), nil
}
func (w *WeType) Stream(ctx context.Context, in domain.StreamRequest, frames <-chan []byte, emit func(domain.Event) error) (domain.Transcript, error) {
	c, closeWS, e := w.open(ctx, in.UserID)
	if e != nil {
		return domain.Transcript{}, e
	}
	defer closeWS()
	id := strings.ReplaceAll(in.SessionID, "-", "")
	var seq, total uint64
	var text string
	ordinaryFinal := false
	batch := make([][]byte, 0, 6)
	upload := func(end bool) error {
		seq++
		var b []byte
		if seq == 1 {
			b = append([]byte("#!OPUS_RAW_V1"), 2, 1, 0)
		}
		for _, f := range batch {
			b = binary.LittleEndian.AppendUint16(b, uint16(len(f)))
			b = append(b, f...)
		}
		total += uint64(len(b))
		current, _, e := c.voice(id, b, seq, total, end)
		if e != nil {
			return e
		}
		if current != "" {
			text = current
			if end {
				ordinaryFinal = true
			}
			if e = emit(domain.Event{Type: "transcript", Text: text}); e != nil {
				return e
			}
		}
		batch = batch[:0]
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return domain.Transcript{}, ctx.Err()
		case f, ok := <-frames:
			if !ok {
				if e = upload(true); e != nil {
					return domain.Transcript{}, e
				}
				for i := 0; i < 8 && !ordinaryFinal; i++ {
					select {
					case <-ctx.Done():
						return domain.Transcript{}, ctx.Err()
					case <-time.After(250 * time.Millisecond):
					}
					current, _, e := c.voice(id, nil, 0, total, true)
					if e != nil {
						return domain.Transcript{}, e
					}
					if current != "" {
						text = current
						ordinaryFinal = true
					}
				}
				// A polished-only response cannot prove literal transcription.
				// Preserve emitted ordinary partials and report an uncertain finish.
				if !ordinaryFinal {
					return domain.Transcript{}, domain.ErrUncertain
				}
				return domain.Transcript{Text: text, Final: true}, nil
			}
			batch = append(batch, f)
			if len(batch) == 6 {
				if e = upload(false); e != nil {
					return domain.Transcript{}, e
				}
			}
		}
	}
}
