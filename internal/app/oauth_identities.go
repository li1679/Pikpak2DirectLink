package app

import (
	"errors"
	"strings"
	"time"
)

// upsertOAuthUser matches only the provider's immutable namespace and subject.
// An email from an OAuth service never grants access to an email login account.
func (s *userStore) upsertOAuthUser(provider string, profile LinuxDoProfile, allowRegistration bool, now time.Time) (User, bool, error) {
	provider, subject := strings.TrimSpace(provider), strings.TrimSpace(profile.ID)
	if provider == "" || provider == "email" || subject == "" {
		return User{}, false, errors.New("OAuth provider and user id are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, false, err
	}
	defer tx.Rollback()
	identity, exists, err := loadUserIdentity(tx, provider, subject)
	if err != nil {
		return User{}, false, err
	}
	displayName := firstNonEmpty(profile.DisplayName, profile.Name, profile.Username, "OAuth User")
	email, avatar := normalizeEmail(profile.Email), strings.TrimSpace(profile.AvatarURL)
	nowUnix := now.Unix()
	var user User
	if exists {
		var ok bool
		user, ok, err = loadUserByID(tx, identity.userID)
		if err != nil {
			return User{}, false, err
		}
		if !ok {
			return User{}, false, errUserNotFound
		}
		if user.Disabled {
			return User{}, false, errUserDisabled
		}
		if _, err := tx.Exec(`UPDATE users SET email=COALESCE(NULLIF(?, ''),email),display_name=?,avatar_url=?,updated_at=? WHERE id=?`, email, displayName, avatar, nowUnix, user.ID); err != nil {
			return User{}, false, err
		}
		if _, err := tx.Exec(`UPDATE user_identities SET email=?,username=?,display_name=?,avatar_url=?,updated_at=? WHERE provider=? AND provider_user_id=?`, email, profile.Username, displayName, avatar, nowUnix, provider, subject); err != nil {
			return User{}, false, err
		}
		if email != "" {
			user.Email = email
		}
		user.DisplayName, user.AvatarURL, user.UpdatedAt = displayName, avatar, nowUnix
	} else {
		if !allowRegistration {
			return User{}, false, errOAuthRegistrationClosed
		}
		user = User{ID: newUserID(), Email: email, DisplayName: displayName, AvatarURL: avatar, CreatedAt: nowUnix, UpdatedAt: nowUnix}
		if _, err := tx.Exec(`INSERT INTO users(id,email,display_name,avatar_url,disabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, user.ID, email, displayName, avatar, 0, nowUnix, nowUnix); err != nil {
			return User{}, false, err
		}
		if _, err := tx.Exec(`INSERT INTO user_identities(provider,provider_user_id,user_id,email,username,display_name,avatar_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, provider, subject, user.ID, email, profile.Username, displayName, avatar, nowUnix, nowUnix); err != nil {
			return User{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, false, err
	}
	return user, !exists, nil
}

func (s *userStore) oauthIdentityExists(provider, subject string) (bool, error) {
	_, ok, err := loadUserIdentity(s.db, strings.TrimSpace(provider), strings.TrimSpace(subject))
	return ok, err
}
