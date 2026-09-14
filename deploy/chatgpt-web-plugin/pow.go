package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha3"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strconv"
	"time"
)

const (
	powDefaultScript = "https://chatgpt.com/backend-api/sentinel/sdk.js"
	powLegacyPrefix  = "gAAAAAC"
	powProofPrefix   = "gAAAAAB"
	powSolveLimit    = 500000
)

var (
	powCores     = []int{8, 16, 24, 32}
	powScreens   = [][2]int{{1920, 1080}, {1440, 900}, {2560, 1440}, {3840, 2160}}
	powDocument  = []string{"__reactContainer$fzelfjyxej8", "_reactListening5dehydibo78", "location"}
	powNavigator = []string{
		"registerProtocolHandler−function registerProtocolHandler() { [native code] }",
		"storage−[object StorageManager]",
		"locks−[object LockManager]",
		"appCodeName−Mozilla",
		"permissions−[object Permissions]",
		"share−function share() { [native code] }",
		"webdriver−false",
		"managed−[object NavigatorManagedData]",
		"canShare−function canShare() { [native code] }",
		"vendor−Google Inc.",
		"mediaDevices−[object MediaDevices]",
		"vibrate−function vibrate() { [native code] }",
		"storageBuckets−[object StorageBucketManager]",
		"mediaCapabilities−[object MediaCapabilities]",
		"cookieEnabled−true",
		"virtualKeyboard−[object VirtualKeyboard]",
		"product−Gecko",
		"presentation−[object Presentation]",
		"onLine−true",
		"mimeTypes−[object MimeTypeArray]",
		"credentials−[object CredentialsContainer]",
		"serviceWorker−[object ServiceWorkerContainer]",
		"keyboard−[object Keyboard]",
		"gpu−[object GPU]",
		"doNotTrack",
		"serial−[object Serial]",
		"pdfViewerEnabled−true",
		"language−zh-CN",
		"geolocation−[object Geolocation]",
		"userAgentData−[object NavigatorUAData]",
		"getUserMedia−function getUserMedia() { [native code] }",
		"sendBeacon−function sendBeacon() { [native code] }",
		"hardwareConcurrency−32",
		"windowControlsOverlay−[object WindowControlsOverlay]",
	}
	powWindow = []string{
		"0", "window", "self", "document", "name", "location", "customElements", "history",
		"navigation", "innerWidth", "innerHeight", "scrollX", "scrollY", "visualViewport",
		"screenX", "screenY", "outerWidth", "outerHeight", "devicePixelRatio", "screen",
		"chrome", "navigator", "onresize", "performance", "crypto", "indexedDB",
		"sessionStorage", "localStorage", "scheduler", "alert", "atob", "btoa", "fetch",
		"matchMedia", "postMessage", "queueMicrotask", "requestAnimationFrame", "setInterval",
		"setTimeout", "caches", "__NEXT_DATA__", "__BUILD_MANIFEST", "__NEXT_PRELOADREADY",
	}
)

var powProcessStart = time.Now()

func powPickIndex(size int) int {
	if size <= 1 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(size)))
	if err != nil {
		return 0
	}
	return int(value.Int64())
}

func powRandomFloat() float64 {
	const precision = int64(1) << 53
	value, err := rand.Int(rand.Reader, big.NewInt(precision))
	if err != nil {
		return 0.5
	}
	return float64(value.Int64()) / float64(precision)
}

// powParseTime reproduces the browser's Date.toString() in US Eastern time, which
// the sentinel service expects verbatim in the configuration array.
func powParseTime() string {
	eastern := time.FixedZone("Eastern Standard Time", -5*60*60)
	return time.Now().In(eastern).Format("Mon Jan 02 2006 15:04:05") + " GMT-0500 (Eastern Standard Time)"
}

// powConfiguration builds the 25-element browser environment array the sentinel
// proof-of-work is computed over. Index 3 and index 9 are placeholders that the
// solver replaces with the candidate counter.
func powConfiguration(userAgent string, scriptSources []string, dataBuild string) []interface{} {
	screen := powScreens[powPickIndex(len(powScreens))]
	script := powDefaultScript
	if len(scriptSources) > 0 {
		script = scriptSources[powPickIndex(len(scriptSources))]
	}
	elapsed := float64(time.Since(powProcessStart).Nanoseconds()) / 1e6
	return []interface{}{
		screen[0] + screen[1],
		powParseTime(),
		4294705152,
		1,
		userAgent,
		script,
		dataBuild,
		"en-US",
		"en-US,es-US,en,es",
		powRandomFloat(),
		powNavigator[powPickIndex(len(powNavigator))],
		powDocument[powPickIndex(len(powDocument))],
		powWindow[powPickIndex(len(powWindow))],
		elapsed,
		newDeviceID(),
		"",
		powCores[powPickIndex(len(powCores))],
		float64(time.Now().UnixMilli()) - elapsed,
		0, 0, 0, 0, 0, 0,
		0,
	}
}

// powCompactJSON matches Python's json.dumps(separators=(",", ":"), ensure_ascii=False),
// which the upstream solver output must be byte-compatible with.
func powCompactJSON(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, failure(500, "web_pow_encoding_failed")
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

func powLegacyToken(userAgent string, scriptSources []string, dataBuild string) (string, error) {
	raw, err := powCompactJSON(powConfiguration(userAgent, scriptSources, dataBuild))
	if err != nil {
		return "", err
	}
	return powLegacyPrefix + base64.StdEncoding.EncodeToString(raw), nil
}

func powProofToken(seed, difficulty, userAgent string, scriptSources []string, dataBuild string) (string, error) {
	answer, err := powSolve(seed, difficulty, powConfiguration(userAgent, scriptSources, dataBuild))
	if err != nil {
		return "", err
	}
	return powProofPrefix + answer, nil
}

// powSolve searches for a counter whose SHA3-512 digest over seed+base64(config)
// starts below the requested difficulty target.
func powSolve(seed, difficulty string, configuration []interface{}) (string, error) {
	target, err := hex.DecodeString(difficulty)
	if err != nil || len(target) == 0 || len(target) > 64 {
		return "", failure(502, "web_pow_difficulty_invalid")
	}
	head, err := powCompactJSON(configuration[:3])
	if err != nil {
		return "", err
	}
	middle, err := powCompactJSON(configuration[4:9])
	if err != nil {
		return "", err
	}
	tail, err := powCompactJSON(configuration[10:])
	if err != nil {
		return "", err
	}
	if len(head) < 2 || len(middle) < 2 || len(tail) < 2 {
		return "", failure(500, "web_pow_encoding_failed")
	}

	prefix := make([]byte, 0, len(head))
	prefix = append(prefix, head[:len(head)-1]...)
	prefix = append(prefix, ',')

	infix := make([]byte, 0, len(middle)+1)
	infix = append(infix, ',')
	infix = append(infix, middle[1:len(middle)-1]...)
	infix = append(infix, ',')

	suffix := make([]byte, 0, len(tail))
	suffix = append(suffix, ',')
	suffix = append(suffix, tail[1:]...)

	seedBytes := []byte(seed)
	body := make([]byte, 0, len(prefix)+len(infix)+len(suffix)+32)
	digestInput := make([]byte, 0, len(seedBytes)+1024)

	for counter := 0; counter < powSolveLimit; counter++ {
		body = body[:0]
		body = append(body, prefix...)
		body = strconv.AppendInt(body, int64(counter), 10)
		body = append(body, infix...)
		body = strconv.AppendInt(body, int64(counter>>1), 10)
		body = append(body, suffix...)

		encoded := base64.StdEncoding.EncodeToString(body)
		digestInput = digestInput[:0]
		digestInput = append(digestInput, seedBytes...)
		digestInput = append(digestInput, encoded...)
		digest := sha3.Sum512(digestInput)
		if bytes.Compare(digest[:len(target)], target) <= 0 {
			return encoded, nil
		}
	}
	return "", failure(502, "web_pow_unsolved")
}
