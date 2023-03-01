package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"log"
	"time"
)

const (
	TOKEN_MAX_TTL      = 3 * 24 * time.Hour
	COOKIE_MAX_TTL int = 3 * 24 * 60 * 60
)

func GetSecretKey() *[32]byte {
	key, err := hex.DecodeString(gitcodeCfg.Secret)
	if err != nil {
		log.Fatal(err)
	}
	return (*[32]byte)(key)
}

func GenerateToken(username string) (string, error) {
	bytes := make([]byte, len(username)+8)
	binary.BigEndian.PutUint64(bytes, uint64(time.Now().Unix()))
	copy(bytes[8:], []byte(username))
	if tokenBytes, err := Encrypt(bytes, GetSecretKey()); err != nil {
		return "", err
	} else {
		return base64.RawStdEncoding.EncodeToString(tokenBytes), nil
	}
}

func ParseToken(token string) (string, bool, error) {
	if tokenBytes, err := base64.RawStdEncoding.DecodeString(token); err != nil {
		return "", false, err
	} else if bytes, err := Decrypt(tokenBytes, GetSecretKey()); err != nil {
		return "", false, err
	} else {
		genTime := binary.BigEndian.Uint64(bytes[:8])
		username := string(bytes[8:])
		expired := time.Since(time.Unix(int64(genTime), 0)) > TOKEN_MAX_TTL
		return username, expired, nil
	}
}
