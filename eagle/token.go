// SPDX-License-Identifier: AGPL-3.0-only
package eagle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

type ticketClaims struct {
	ExpiresAt string `json:"expires_at"`
	ServerEnv string `json:"server_env"`
	ServerID  string `json:"server_id"`
	UserID    string `json:"user_id"`
}

type ticketEnvelope struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
	Version   int             `json:"version"`
}

var errTicket = errors.New("invalid Eagle ticket")

func signTicket(key []byte, gid uint32, pid uint64, expires time.Time) string {
	payload, _ := json.Marshal(ticketClaims{
		ExpiresAt: strconv.FormatInt(expires.Unix(), 10), ServerEnv: "lp1",
		ServerID: strconv.FormatUint(uint64(gid), 10), UserID: fmt.Sprintf("%016x", pid),
	})
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	envelope, _ := json.Marshal(ticketEnvelope{
		Payload: payload, Signature: base64.StdEncoding.EncodeToString(mac.Sum(nil)), Version: 1,
	})
	return base64.StdEncoding.EncodeToString(envelope)
}

func verifyTicket(key []byte, token string, gid uint32, now time.Time) (uint64, error) {
	if len(token) > 8192 {
		return 0, errTicket
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, errTicket
	}
	var envelope ticketEnvelope
	if json.Unmarshal(data, &envelope) != nil || envelope.Version != 1 {
		return 0, errTicket
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	mac := hmac.New(sha256.New, key)
	mac.Write(envelope.Payload)
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return 0, errTicket
	}
	var claims ticketClaims
	if json.Unmarshal(envelope.Payload, &claims) != nil || claims.ServerEnv != "lp1" ||
		claims.ServerID != strconv.FormatUint(uint64(gid), 10) || len(claims.UserID) != 16 {
		return 0, errTicket
	}
	expires, err := strconv.ParseInt(claims.ExpiresAt, 10, 64)
	if err != nil || expires <= now.Unix() {
		return 0, errTicket
	}
	pid, err := strconv.ParseUint(claims.UserID, 16, 64)
	if err != nil || pid == 0 {
		return 0, errTicket
	}
	return pid, nil
}
