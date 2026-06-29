package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Claims struct {
	UserID   int64
	Username string
	Expires  int64
}

func Sign(secret string, userID int64, username string, ttl time.Duration) (string, error) {
	exp := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%d:%s:%d", userID, username, exp)
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payloadB64))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payloadB64 + "." + sig, nil
}

func Parse(secret, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return nil, errors.New("invalid signature")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(payloadBytes), ":")
	if len(fields) != 3 {
		return nil, errors.New("invalid payload")
	}
	uid, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil, err
	}
	exp, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return nil, err
	}
	if time.Now().Unix() > exp {
		return nil, errors.New("token expired")
	}
	return &Claims{UserID: uid, Username: fields[1], Expires: exp}, nil
}
