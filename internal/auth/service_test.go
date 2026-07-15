package auth

import (
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("password was not accepted")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("wrong password was accepted")
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	service := New("01234567890123456789012345678901", time.Minute, time.Hour)
	token, _, err := service.NewAccessToken("user-id", "device-id")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.ParseAccessToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-id" || claims.DeviceID != "device-id" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}
