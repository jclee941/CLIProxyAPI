package main

import (
	"math"
	"strings"
)

// An account is identified by its auth id everywhere it is routed, pinned, owned
// or stored. What a response and the video_turn_diag line call it is a separate
// thing: a readable name taken from the account's label, so a caller's log can
// say which Google account served a turn. The name is a display value only and
// is never read back as an identity.

const accountDisplayNameLimit = 32

// accountDisplayName turns a label into the name shown for the account: the
// local part of an email address or the label itself, trimmed and lowercased.
// Any character outside lowercase letters, digits, '.', '_' and '-' becomes
// '-', the name is cut to 32 characters, and it must start with a letter or
// digit (^[a-z0-9][a-z0-9._-]{0,31}$). A label that leaves nothing usable gives
// "", and the caller falls back to the short id.
func accountDisplayName(label string) string {
	name := strings.TrimSpace(label)
	if local, domain, found := strings.Cut(name, "@"); found && local != "" && domain != "" {
		name = local
	}
	name = strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9',
			character == '.', character == '_', character == '-':
			builder.WriteRune(character)
		default:
			builder.WriteByte('-')
		}
	}
	name = strings.TrimLeft(builder.String(), "._-")
	if len(name) > accountDisplayNameLimit {
		name = name[:accountDisplayNameLimit]
	}
	return name
}

// rememberAccount keeps the display name of every record the plugin resolves or
// lists, so a response can name the account without asking the host again.
func (service *service) rememberAccount(record storageRecord) {
	if record.ID == "" {
		return
	}
	name := accountDisplayName(record.Label)
	service.accountNamesMu.Lock()
	defer service.accountNamesMu.Unlock()
	if name == "" {
		delete(service.accountNames, record.ID)
		return
	}
	if service.accountNames == nil {
		service.accountNames = make(map[string]string)
	}
	service.accountNames[record.ID] = name
}

// accountName is the display name of an account: its remembered label name, the
// short id when no label was ever seen for it, and nothing for no account.
func (service *service) accountName(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	service.accountNamesMu.RLock()
	name := service.accountNames[id]
	service.accountNamesMu.RUnlock()
	if name != "" {
		return name
	}
	return diagAccount(id)
}

// recordAccountName remembers a record the plugin is about to use and returns
// its display name.
func (service *service) recordAccountName(record storageRecord) string {
	service.rememberAccount(record)
	return service.accountName(record.ID)
}

// noteExecutorAccount remembers the executing account's label from the record
// the host sent with the request, and asks the host for the record when the
// request carried none, so a response never has to name an account by its
// short id while a label is available.
func (service *service) noteExecutorAccount(request executorRequest) {
	if len(request.StorageJSON) > 0 {
		if _, err := service.parseStorage(request.StorageJSON, false); err == nil {
			return
		}
	}
	service.accountNamesMu.RLock()
	_, known := service.accountNames[request.AuthID]
	service.accountNamesMu.RUnlock()
	if known || request.HostCallbackID == "" {
		return
	}
	if _, _, err := service.findRecord(request.HostCallbackID, request.AuthID); err != nil {
		return
	}
}

// accountUsage is how much of the five hour and weekly windows the account had
// used when it was last read, in percent with one decimal. A window never read
// has no key, and an account never read has none. It reads the cache the
// scheduler routes on and asks Google nothing.
func (service *service) accountUsage(id string) map[string]float64 {
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	if !found {
		return nil
	}
	usage := make(map[string]float64, 2)
	if snapshot.roomUsedSeen {
		if percent, ok := usagePercent(snapshot.roomUsed); ok {
			usage["5h"] = percent
		}
	}
	if snapshot.weekUsedSeen {
		if percent, ok := usagePercent(snapshot.weekUsed); ok {
			usage["weekly"] = percent
		}
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func usagePercent(fraction float64) (float64, bool) {
	if math.IsNaN(fraction) || math.IsInf(fraction, 0) {
		return 0, false
	}
	return math.Min(100, math.Max(0, math.Round(fraction*1000)/10)), true
}

// accountDisplay is what a response says about the account that served a turn.
type accountDisplay struct {
	Name  string
	Usage map[string]float64
}

// accountDisplay is empty for a turn no account was chosen for.
func (service *service) accountDisplay(id string) accountDisplay {
	name := service.accountName(id)
	if name == "" {
		return accountDisplay{}
	}
	return accountDisplay{Name: name, Usage: service.accountUsage(id)}
}

// put sets `account` and `account_usage` on a response object, when there is an
// account to name.
func (display accountDisplay) put(target map[string]any) {
	if display.Name == "" {
		return
	}
	target["account"] = display.Name
	if len(display.Usage) > 0 {
		target["account_usage"] = display.Usage
	}
}
