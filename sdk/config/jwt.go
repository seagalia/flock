package config

import (
	"crypto"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type JwtAlg struct {
	AlgRaw string
	Alg    jwt.SigningMethod
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (d *JwtAlg) UnmarshalYAML(unmarshal func(any) error) error {
	var algRaw string
	if err := unmarshal(&algRaw); err != nil {
		return err
	}
	if algRaw == "" {
		return nil
	}
	signingMethod := jwt.GetSigningMethod(algRaw)
	if signingMethod == nil {
		return fmt.Errorf("algorithm '%s' doesn't exists.", algRaw)
	}
	d.AlgRaw = algRaw
	d.Alg = signingMethod
	return nil
}

// MarshalYAML implements the yaml.Marshaler interface, it writes the algorithm name UnmarshalYAML reads.
func (d JwtAlg) MarshalYAML() (any, error) {
	return d.AlgRaw, nil
}

type JwtMaker struct {
	Alg        JwtAlg   `yaml:"alg"`
	PrivateKey string   `yaml:"private_key"`
	ExpireIn   Duration `yaml:"expire_in"`
	Issuer     string   `yaml:"issuer"`
	Audience   string   `yaml:"audience"`
}

func (l *JwtMaker) UnmarshalYAML(unmarshal func(any) error) error {
	type plain JwtMaker
	err := unmarshal((*plain)(l))
	if err != nil {
		return err
	}
	if l.PrivateKey == "" {
		return fmt.Errorf("jwt config: private key is required")
	}
	if l.ExpireIn <= 0 {
		l.ExpireIn = Duration(time.Hour * 3)
	}
	if l.Audience == "" {
		l.Audience = "flock"
	}
	if l.Issuer == "" {
		l.Issuer = "flock"
	}
	if l.Alg.Alg == nil {
		return fmt.Errorf("jwt config: algorithm is required")
	}
	// a key that does not parse must stop the controller and check-config, not fail every login afterwards
	_, _, err = l.Keys()
	return err
}

// Keys gives the key signing the tokens and the one verifying them: the private key and its public half, or the same
// secret for HS algorithms
func (l *JwtMaker) Keys() (any, any, error) {
	alg := l.Alg.Alg.Alg()
	pemKey := []byte(l.PrivateKey)
	if strings.HasPrefix(alg, "HS") {
		return pemKey, pemKey, nil
	}
	var privateKey crypto.Signer
	var err error
	switch {
	case strings.HasPrefix(alg, "ES"):
		privateKey, err = jwt.ParseECPrivateKeyFromPEM(pemKey)
	case alg == jwt.SigningMethodEdDSA.Alg():
		var edKey crypto.PrivateKey
		edKey, err = jwt.ParseEdPrivateKeyFromPEM(pemKey)
		privateKey, _ = edKey.(crypto.Signer) //nolint:errcheck
	default:
		privateKey, err = jwt.ParseRSAPrivateKeyFromPEM(pemKey)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("jwt_maker private key for %s: %w", alg, err)
	}
	return privateKey, privateKey.Public(), nil
}
