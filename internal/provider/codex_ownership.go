package provider

import (
	"errors"
	"os"
)

// Imported Codex credentials can also be held by Codex Router or CC Switch.
// Only the owner may rotate or replace them. Unknown values fail closed.
// Managed mode is for an isolated sign-in with no other refresh owner.
func codexAuthManaged() bool {
	return os.Getenv("MAGPIE_CODEX_AUTH_OWNER") == "magpie"
}

var errCodexAuthExternal = errors.New("Codex sign-in is managed externally; refresh or switch the account in Codex or your account manager, then retry (MAGPIE_CODEX_AUTH_OWNER=magpie is only for an exclusively managed sign-in)")
