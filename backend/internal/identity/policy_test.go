package identity

import (
	"errors"
	"testing"
	"time"
)

func TestPolicy(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	expired := now
	future := now.Add(time.Second)
	tests := []struct {
		name   string
		user   User
		action Action
		owner  string
		want   error
	}{
		{"ordinary cannot upload", User{ID: "a", Role: RoleStandard}, UploadResume, "", ErrForbidden},
		{"ordinary cannot recommend", User{ID: "a", Role: RoleStandard}, CreateRecommendations, "", ErrForbidden},
		{"vip long term", User{ID: "a", Role: RoleVIP}, ReadResume, "a", nil},
		{"vip future", User{ID: "a", Role: RoleVIP, VIPExpiresAt: &future}, ReadRecommendations, "a", nil},
		{"vip at exact expiry", User{ID: "a", Role: RoleVIP, VIPExpiresAt: &expired}, ReadRecommendations, "a", ErrForbidden},
		{"expired can delete own data", User{ID: "a", Role: RoleVIP, VIPExpiresAt: &expired}, DeleteResume, "a", nil},
		{"vip cannot read other resume", User{ID: "a", Role: RoleVIP}, ReadResume, "b", ErrNotFound},
		{"ordinary cannot delete other resume", User{ID: "a", Role: RoleStandard}, DeleteResume, "b", ErrNotFound},
		{"administrator cross owner", User{ID: "a", Role: RoleAdmin}, ReadResume, "b", nil},
		{"disabled administrator", User{ID: "a", Role: RoleAdmin, Disabled: true}, ManageUsers, "", ErrUnauthenticated},
		{"unknown action fails closed", User{ID: "a", Role: RoleAdmin}, Action("unknown"), "", ErrForbidden},
		{"unknown role fails closed", User{ID: "a", Role: Role("unexpected")}, BrowseJobs, "", ErrUnauthenticated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Authorize(tc.user, tc.action, tc.owner, now)
			if !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
