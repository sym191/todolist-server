package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidToken = errors.New("invalid token")

type Claims struct {
	DeviceID string `json:"device_id"`
	Type     string `json:"typ"`
	jwt.RegisteredClaims
}

type Service struct {
	secret          []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	now             func() time.Time
}

func New(secret string, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{
		secret:          []byte(secret),
		accessTokenTTL:  accessTTL,
		refreshTokenTTL: refreshTTL,
		now:             time.Now,
	}
}

func HashPassword(password string) (string, error) {
	if len(password) < 8 || len(password) > 128 {
		return "", errors.New("password must contain between 8 and 128 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(hash), err
}

func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Service) NewAccessToken(userID, deviceID string) (string, time.Time, error) {
	now := s.now().UTC()
	expiresAt := now.Add(s.accessTokenTTL)
	claims := Claims{
		DeviceID: deviceID,
		Type:     "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	return token, expiresAt, err
}

func (s *Service) ParseAccessToken(raw string) (Claims, error) {
	claims := Claims{}
	token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %s", token.Method.Alg())
		}
		return s.secret, nil
	}, jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid || claims.Type != "access" || claims.Subject == "" || claims.DeviceID == "" {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}

func (s *Service) NewRefreshToken() (plain string, hash []byte, expiresAt time.Time, err error) {
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		return "", nil, time.Time{}, err
	}
	plain = base64.RawURLEncoding.EncodeToString(bytes)
	digest := sha256.Sum256([]byte(plain))
	return plain, digest[:], s.now().UTC().Add(s.refreshTokenTTL), nil
}

func HashRefreshToken(raw string) []byte {
	digest := sha256.Sum256([]byte(raw))
	return digest[:]
}
