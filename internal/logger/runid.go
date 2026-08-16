package logger

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

// NewRunID returns a short identifier for one weft invocation, used to tie
// every log line from that invocation together.
//
// Randomness comes first so two runs starting in the same millisecond cannot
// collide. The timestamp fallback exists because a run ID is a convenience: if
// the entropy source is unavailable, a slightly weaker ID is far better than
// failing the command.
//
// cf. Java: UUID.randomUUID().toString().substring(0, 16), but 8 random bytes
// is plenty to separate runs within one log file and keeps lines readable.
func NewRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}
