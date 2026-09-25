package pluginhost

import "strings"

type frontendHTTPSegment struct {
	literal  string
	param    string
	suffix   string
	ginParam bool
	catchAll bool
}

func parseFrontendHTTPPattern(path string, reserved bool) ([]frontendHTTPSegment, bool) {
	if !strings.HasPrefix(path, "/") || len(path) > 1024 || path == "/" {
		return nil, false
	}
	if !reserved && (path == managementBasePath || strings.HasPrefix(path, managementBasePath+"/") || path == "/v0/resource" || strings.HasPrefix(path, "/v0/resource/")) {
		return nil, false
	}
	parts := strings.Split(path[1:], "/")
	if len(parts) > 32 {
		return nil, false
	}
	segments := make([]frontendHTTPSegment, 0, len(parts))
	params := make(map[string]bool)
	for i, part := range parts {
		segment := frontendHTTPSegment{literal: part}
		if reserved && strings.HasPrefix(part, "*") {
			segment = frontendHTTPSegment{catchAll: true}
		} else if reserved && strings.HasPrefix(part, ":") {
			segment = frontendHTTPSegment{ginParam: true}
		} else if strings.HasPrefix(part, "{") {
			name, suffix, found := strings.Cut(part[1:], "}")
			if !found || i == 0 || !frontendHTTPParamName(name) || params[name] || (suffix != "" && (!strings.HasPrefix(suffix, ":") || !frontendHTTPAtom(suffix[1:]))) {
				return nil, false
			}
			params[name] = true
			segment = frontendHTTPSegment{param: name, suffix: suffix}
		} else if !frontendHTTPLiteral(part) {
			return nil, false
		}
		segments = append(segments, segment)
	}
	return segments, true
}

func frontendHTTPAtom(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '~' || c == '-') {
			return false
		}
	}
	return true
}

func frontendHTTPLiteral(value string) bool {
	base, action, found := strings.Cut(value, ":")
	return frontendHTTPAtom(base) && (!found || frontendHTTPAtom(action))
}

func frontendHTTPParamName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (s frontendHTTPSegment) match(value string) (string, bool) {
	if s.ginParam {
		return value, value != ""
	}
	if s.param == "" {
		return "", s.literal == value
	}
	if !strings.HasSuffix(value, s.suffix) {
		return "", false
	}
	value = strings.TrimSuffix(value, s.suffix)
	return value, frontendHTTPAtom(value)
}

func frontendHTTPPatternsOverlap(a, b []frontendHTTPSegment) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		left, right := a[i], b[i]
		if left.catchAll || right.catchAll {
			return true
		}
		if left.literal != "" {
			if _, ok := right.match(left.literal); !ok {
				return false
			}
		} else if right.literal != "" {
			if _, ok := left.match(right.literal); !ok {
				return false
			}
		} else if !left.ginParam && !right.ginParam && left.suffix != right.suffix {
			return false
		}
	}
	return len(a) == len(b)
}

func matchFrontendHTTPPath(segments []frontendHTTPSegment, path string) (map[string]string, bool) {
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	parts := strings.Split(path[1:], "/")
	if len(parts) != len(segments) {
		return nil, false
	}
	params := make(map[string]string)
	for i, part := range parts {
		value, ok := segments[i].match(part)
		if !ok {
			return nil, false
		}
		if segments[i].param != "" {
			params[segments[i].param] = value
		}
	}
	return params, true
}
