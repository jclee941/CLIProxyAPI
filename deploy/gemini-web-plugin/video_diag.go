package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Video turns on some accounts are answered with the product's canned text-model
// reply instead of a video. Nothing about what was sent or what came back was
// recorded, so the per-account inputs (the selected capability, the advertised
// list, the framing) could not be set against the outcomes. One line per turn
// records both, and only layout and identifiers: never the prompt, a
// conversation or reply id, a URL, a credential or an address. The reply text is
// kept to a short head, and only for a turn that came back without a video.

// videoDiagStashLimit bounds the turns waiting for a later recovery to settle
// them. A turn that is never settled is not worth remembering forever.
const videoDiagStashLimit = 256

type videoTurnDiag struct {
	account  string
	kind     string
	model    capability
	capacity int
	thinking int
	chip     int
	caps     []capability
	shape    string
	slots    string
	s9v      string
	hasS9v   bool
	text     string
}

func newVideoTurnDiag(accountName, kind string, account webAccount, model capability, framing omniFraming, thinking int) *videoTurnDiag {
	return &videoTurnDiag{
		account:  diagName(accountName),
		kind:     kind,
		model:    model,
		capacity: webCapacity(account.CapacityFlags),
		thinking: thinking,
		chip:     framing.chip,
		caps:     account.Capabilities,
	}
}

// diagName is what the video_turn_diag line prints for an account: its readable
// name, or "none" when no account was chosen.
func diagName(name string) string {
	if name == "" {
		return "none"
	}
	return name
}

// diagAccount shortens an account id to six hex characters. An id that is not
// plain hex is hashed first. It is the name an account has only while no label
// is known for it.
func diagAccount(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return "none"
	}
	if !isHexString(id) {
		digest := sha256.Sum256([]byte(id))
		return hex.EncodeToString(digest[:])[:6]
	}
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

func isHexString(value string) bool {
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return value != ""
}

// observe records what a response candidate looked like. The layout of the first
// one is kept; the text of the latest one is, because it is the one that ends
// the turn. It reads and never changes the candidate.
func (diag *videoTurnDiag) observe(candidate any) {
	if diag == nil || candidate == nil {
		return
	}
	if diag.shape == "" {
		diag.shape = videoCandidateShape(candidate)
		diag.slots = videoSlotSkeletons(candidate)
		// Slot 9 on a refusal is a two-character string; whether it is a language
		// or a reason code is only known from its value. Only a string of at most
		// four runes is kept, so nothing the model said can ride along.
		if value, ok := jsonField(candidate, 9).(string); ok && len([]rune(value)) <= 4 {
			diag.s9v, diag.hasS9v = value, true
		}
	}
	if text, ok := jsonField(candidate, 1, 0).(string); ok {
		diag.text = text
	}
}

// observeFrame does the same for a raw generation line, for turns whose stream is
// read frame by frame.
func (diag *videoTurnDiag) observeFrame(raw []byte) {
	if diag == nil {
		return
	}
	frames, err := webResponseFrames(raw)
	if err != nil {
		return
	}
	for _, frame := range frames {
		diag.observe(jsonField(frame, 4, 0))
	}
}

// videoCandidateShape lists the top-level slots of a candidate that carry
// something, and the progress flag in slot 8 when it is a number.
func videoCandidateShape(candidate any) string {
	list, ok := candidate.([]any)
	if !ok {
		return "none"
	}
	slots := make([]string, 0, len(list))
	for index, value := range list {
		if value != nil {
			slots = append(slots, strconv.Itoa(index))
		}
	}
	shape := strings.Join(slots, ",")
	if number, ok := jsonField(candidate, 8, 0).(float64); ok {
		shape += " c8_0=" + strconv.FormatFloat(number, 'f', -1, 64)
	}
	return shape
}

// Skeletons show the form of a candidate slot and nothing it says: numbers,
// booleans and null stay as they are, every string becomes its length in
// runes, and nesting stops at a fixed depth and the whole is cut at a fixed
// length. Object keys are strings, so they are reduced the same way.
const (
	skeletonDepth = 5
	skeletonLimit = 240
)

// videoSlotSkeletons renders the slots that tell a refusal's candidate apart
// from a video's. It reads and never changes the candidate.
func videoSlotSkeletons(candidate any) string {
	parts := make([]string, 0, 3)
	for _, slot := range []int{9, 37, 28} {
		parts = append(parts, "s"+strconv.Itoa(slot)+"="+videoSkeleton(jsonField(candidate, slot)))
	}
	return strings.Join(parts, " ")
}

func videoSkeleton(value any) string {
	var out strings.Builder
	writeSkeleton(&out, value, 0)
	if runes := []rune(out.String()); len(runes) > skeletonLimit {
		return string(runes[:skeletonLimit])
	}
	return out.String()
}

func writeSkeleton(out *strings.Builder, value any, depth int) {
	if out.Len() > skeletonLimit {
		return
	}
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(typed))
	case float64:
		out.WriteString(strconv.FormatFloat(typed, 'f', -1, 64))
	case string:
		out.WriteString("s" + strconv.Itoa(len([]rune(typed))))
	case []any:
		if depth >= skeletonDepth {
			out.WriteString("[..]")
			return
		}
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			writeSkeleton(out, item, depth+1)
		}
		out.WriteByte(']')
	case map[string]any:
		if depth >= skeletonDepth {
			out.WriteString("{..}")
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				out.WriteByte(',')
			}
			out.WriteString("s" + strconv.Itoa(len([]rune(key))) + ":")
			writeSkeleton(out, typed[key], depth+1)
		}
		out.WriteByte('}')
	default:
		out.WriteString("?")
	}
}

// continuationDiagKind is the kind a settling request can tell on its own: a turn
// with a parent was appended to an existing conversation.
func continuationDiagKind(parent string) string {
	if parent != "" {
		return "extension"
	}
	return "first"
}

func shortID(identifier string) string {
	if runes := []rune(identifier); len(runes) > 8 {
		return string(runes[:8])
	}
	return identifier
}

func videoOutcome(err error) string {
	if err == nil {
		return "video"
	}
	if code := safeCredentialCode(err); code != "no_video_generated" {
		return code
	}
	return "no_video"
}

// videoDiagFields builds the log fields for one turn. The host formatter prints
// only its own keys, so the details ride inside them: the outcome in error, the
// request side in reason, the advertised list in budget and the response side in
// remote_transport.
func videoDiagFields(diag *videoTurnDiag, outcome string) map[string]any {
	if diag == nil {
		diag = &videoTurnDiag{account: "none", kind: "unknown"}
	}
	caps := make([]string, 0, len(diag.caps))
	for _, entry := range diag.caps {
		caps = append(caps, fmt.Sprintf("%s/%d/%s", strings.Join(strings.Fields(entry.DisplayName), "_"), entry.Mode, shortID(entry.CapabilityID)))
	}
	shape := diag.shape
	if shape == "" {
		shape = "none"
	}
	response := "candidate_shape=" + shape
	if diag.slots != "" {
		response += " " + diag.slots
	}
	if diag.hasS9v {
		response += " s9v=" + diag.s9v
	}
	if outcome == "no_video" {
		head := strings.Join(strings.Fields(diag.text), " ")
		if runes := []rune(head); len(runes) > 40 {
			head = string(runes[:40])
		}
		response += " reply_head=" + head
	}
	return map[string]any{
		"provider": provider,
		"state":    "video_turn_diag",
		"error":    outcome,
		"reason": fmt.Sprintf("account=%s kind=%s capability=%s mode=%d id8=%s capacity=%d thinking=%d chip=%d",
			diag.account, diag.kind, diag.model.DisplayName, diag.model.Mode, shortID(diag.model.CapabilityID),
			diag.capacity, diag.thinking, diag.chip),
		"budget":           "caps=" + strings.Join(caps, ","),
		"remote_transport": response,
	}
}

// reportVideoTurn writes the one line a finished video turn owes. It is safe
// with no diagnostic at all, and a log that cannot be delivered fails nothing.
func (service *service) reportVideoTurn(diag *videoTurnDiag, outcome string) {
	if diag == nil {
		return
	}
	service.report(videoDiagFields(diag, outcome), "gemini-web: video turn")
}

// stashVideoDiag keeps a turn's diagnostic until a later recovery settles it.
func (service *service) stashVideoDiag(key string, diag *videoTurnDiag) {
	if diag == nil {
		return
	}
	service.videoDiagMu.Lock()
	defer service.videoDiagMu.Unlock()
	if service.videoDiags == nil || len(service.videoDiags) >= videoDiagStashLimit {
		service.videoDiags = make(map[string]*videoTurnDiag)
	}
	service.videoDiags[key] = diag
}

// recallVideoDiag returns the stashed diagnostic, or an empty one that still
// names the account and kind when the plugin restarted since the submission.
func (service *service) recallVideoDiag(key, accountID, kind string) *videoTurnDiag {
	service.videoDiagMu.Lock()
	diag := service.videoDiags[key]
	service.videoDiagMu.Unlock()
	if diag != nil {
		return diag
	}
	return &videoTurnDiag{account: diagName(service.accountName(accountID)), kind: kind}
}

func (service *service) dropVideoDiag(key string) {
	service.videoDiagMu.Lock()
	delete(service.videoDiags, key)
	service.videoDiagMu.Unlock()
}
