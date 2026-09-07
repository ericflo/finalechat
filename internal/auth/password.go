// Package auth implements password hashing plus the opaque session and API
// token formats used by Finalechat.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns an argon2id PHC-format string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a PHC-format argon2id hash.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("parse hash parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// HashToken derives the storage form of an opaque secret.
func HashToken(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// randomString returns n characters drawn uniformly from alphabet.
func randomString(n int) (string, error) {
	buf := make([]byte, n)
	out := make([]byte, n)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			// Reject values that would bias the modulo.
			if int(b) >= 62*4 {
				continue
			}
			out[i] = alphabet[int(b)%62]
			i++
			if i == n {
				break
			}
		}
	}
	return string(out), nil
}

// APITokenPrefix is the prefix every agent token starts with so tokens are
// recognisable in logs, secret scanners and shell history.
const APITokenPrefix = "fc_"

// NewAPIToken mints a new agent token. The returned display prefix is safe to
// store in plaintext.
func NewAPIToken() (secret, prefix string, err error) {
	body, err := randomString(40)
	if err != nil {
		return "", "", err
	}
	secret = APITokenPrefix + body
	return secret, secret[:len(APITokenPrefix)+8], nil
}

// NewSessionToken mints an opaque browser session token.
func NewSessionToken() (string, error) {
	return randomString(48)
}

// LooksLikeAPIToken reports whether a bearer credential has the agent token
// shape. It does not validate the token.
func LooksLikeAPIToken(s string) bool {
	return strings.HasPrefix(s, APITokenPrefix) && len(s) == len(APITokenPrefix)+40
}
