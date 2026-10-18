package notification

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var hookPath = regexp.MustCompile(`^/open-apis/bot/v2/hook/[A-Za-z0-9_-]{16,128}$`)

func validWebhook(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "open.feishu.cn" && u.User == nil && !strings.ContainsAny(raw, "?#") && u.RawPath == "" && hookPath.MatchString(u.Path)
}

type channelCipher struct{ aead cipher.AEAD }

func newCipher(key []byte) (*channelCipher, error) {
	if len(key) == 0 {
		return nil, nil
	}
	if len(key) != 32 {
		return nil, errors.New("notification key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &channelCipher{aead: aead}, nil
}

func (c *channelCipher) seal(owner string, secret ChannelSecret) ([]byte, error) {
	if c == nil {
		return nil, ErrUnconfigured
	}
	data, err := json.Marshal(secret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, data, []byte("notification-channel:v1:"+owner)), nil
}
func (c *channelCipher) open(owner string, data []byte) (ChannelSecret, error) {
	var secret ChannelSecret
	if c == nil {
		return secret, ErrUnconfigured
	}
	n := c.aead.NonceSize()
	if len(data) < n {
		return secret, errors.New("invalid channel ciphertext")
	}
	plain, err := c.aead.Open(nil, data[:n], data[n:], []byte("notification-channel:v1:"+owner))
	if err != nil {
		return secret, errors.New("channel decryption failed")
	}
	if err = json.Unmarshal(plain, &secret); err != nil || !validWebhook(secret.Webhook) {
		return ChannelSecret{}, errors.New("invalid encrypted channel")
	}
	return secret, nil
}
