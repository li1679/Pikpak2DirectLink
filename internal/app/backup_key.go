package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ValidatePersonalDatabaseKey checks a personal snapshot without changing it.
func ValidatePersonalDatabaseKey(ctx context.Context, path, key string) error {
	cipher, err := NewSecretCipher(key, nil)
	if err != nil {
		return err
	}
	dsn, err := sqliteReadOnlyDSN(path)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	if err := validatePersonalSchema(db); err != nil {
		return err
	}
	value, ok, err := newAppSecretStore(db, cipher).get(dataKeyCheckName)
	if err != nil {
		return fmt.Errorf("备份密钥不匹配：%w", err)
	}
	if !ok || value != dataKeyCheckPlaintext {
		return errors.New("备份缺少有效的个人版密钥校验记录")
	}
	return nil
}
