package app

import (
	"database/sql"
	"net/http"
	"time"
)

type userLoginMethod struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (s *userStore) createSessionForMethod(userID, providerID, providerName string, now time.Time) (string, error) {
	token, err := generateSessionID()
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	digest := sessionTokenDigest(token)
	if _, err := tx.Exec(`INSERT INTO user_sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`, digest, userID, now.Add(userSessionMaxAge).Unix(), now.Unix()); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO user_session_auth(token_hash,provider_id,provider_name) VALUES(?,?,?)`, digest, providerID, providerName); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Server) userLoginMethod(r *http.Request, userID string) *userLoginMethod {
	if s.db == nil {
		return nil
	}
	cookie, err := r.Cookie(userSessionCookieName)
	if err != nil {
		return nil
	}
	var id, name string
	err = s.db.QueryRow(`SELECT provider_id,provider_name FROM user_session_auth WHERE token_hash=?`, sessionTokenDigest(cookie.Value)).Scan(&id, &name)
	if err == sql.ErrNoRows {
		// Historical sessions did not store a login method. Infer it only when
		// the account has one identity; never label an ambiguous session.
		rows, err := s.db.Query(`SELECT provider FROM user_identities WHERE user_id=? LIMIT 2`, userID)
		if err != nil {
			return nil
		}
		count := 0
		for rows.Next() {
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil
			}
			count++
		}
		rows.Close()
		if count != 1 {
			return nil
		}
	} else if err != nil {
		return nil
	}
	method := &userLoginMethod{ID: id, Name: name, Type: "oauth"}
	if id == "email" {
		method.Name = "邮箱"
		method.Type = "email"
		return method
	}
	if p, err := s.effectiveOAuthProvider(id); err == nil {
		method.Name = p.Name
	}
	if method.Name == "" {
		method.Name = "社区授权"
	}
	return method
}
