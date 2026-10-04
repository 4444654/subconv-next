package authn

import (
	"errors"
	"regexp"

	"golang.org/x/crypto/bcrypt"
)

const DefaultUsername = "admin"

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.@-]{1,64}$`)
var hashPattern = regexp.MustCompile(`^\$2[aby]\$(10|11|12|13|14)\$[./a-zA-Z0-9]{53}$`)

func ValidUsername(username string) bool { return usernamePattern.MatchString(username) }

func HashPassword(password string) (string, error) {
	if len(password) < 8 || len(password) > 72 {
		return "", errors.New("password must contain 8–72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func ValidateHash(hash string) error {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || !hashPattern.MatchString(hash) || cost < bcrypt.DefaultCost || cost > 14 {
		return errors.New("password hash must be a valid bcrypt hash with cost 10–14")
	}
	return nil
}

func VerifyPassword(hash, password string) bool {
	if len(password) > 72 || ValidateHash(hash) != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
