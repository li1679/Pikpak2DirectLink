package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

const (
	linuxDoAuthorizeURL = "https://connect.linux.do/oauth2/authorize"
	linuxDoTokenURL     = "https://connect.linux.do/oauth2/token"
	linuxDoUserURL      = "https://connect.linux.do/api/user"
)

type linuxDoTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func linuxDoProfileFromPayload(raw map[string]any) (LinuxDoProfile, error) {
	payload := raw
	if nested, ok := raw["user"].(map[string]any); ok {
		payload = nested
	}
	profile := LinuxDoProfile{
		ID:          stringFromAny(payload["id"]),
		Username:    firstNonEmpty(stringFromAny(payload["username"]), stringFromAny(payload["login"]), stringFromAny(payload["name"])),
		Name:        stringFromAny(payload["name"]),
		DisplayName: firstNonEmpty(stringFromAny(payload["display_name"]), stringFromAny(payload["nickname"]), stringFromAny(payload["name"]), stringFromAny(payload["username"])),
		Email:       stringFromAny(payload["email"]),
		AvatarURL:   firstNonEmpty(stringFromAny(payload["avatar_url"]), stringFromAny(payload["avatar"]), stringFromAny(payload["picture"])),
	}
	if strings.TrimSpace(profile.ID) == "" {
		return LinuxDoProfile{}, errors.New("LinuxDo user response did not include an id")
	}
	return profile, nil
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}
