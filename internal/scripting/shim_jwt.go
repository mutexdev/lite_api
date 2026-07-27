package scripting

// The sandbox jsonwebtoken shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scalar"

	jwtlib "github.com/golang-jwt/jwt/v5"

	"github.com/dop251/goja"
)

func newScriptJWTObject(runtime *goja.Runtime) *goja.Object {
	jwtObject := runtime.NewObject()
	_ = jwtObject.Set("sign", func(call goja.FunctionCall) goja.Value {
		callback, callbackIndex := scriptOptionalCallback(call.Arguments)
		optionsIndex := 2
		if callbackIndex == 2 {
			optionsIndex = -1
		}
		token, err := scriptJWTSign(call.Argument(0), call.Argument(1), scriptOptionalArgument(call.Arguments, optionsIndex))
		if callback != nil {
			callScriptCallback(runtime, callback, err, runtime.ToValue(token))
			return goja.Undefined()
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(token)
	})
	_ = jwtObject.Set("verify", func(call goja.FunctionCall) goja.Value {
		callback, callbackIndex := scriptOptionalCallback(call.Arguments)
		optionsIndex := 2
		if callbackIndex == 2 {
			optionsIndex = -1
		}
		decoded, err := scriptJWTVerify(call.Argument(0), call.Argument(1), scriptOptionalArgument(call.Arguments, optionsIndex))
		if callback != nil {
			callScriptCallback(runtime, callback, err, runtime.ToValue(decoded))
			return goja.Undefined()
		}
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(decoded)
	})
	_ = jwtObject.Set("decode", func(call goja.FunctionCall) goja.Value {
		decoded, err := scriptJWTDecode(call.Argument(0), call.Argument(1))
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(decoded)
	})
	return jwtObject
}

func scriptJWTSign(payloadValue, secretValue, optionsValue goja.Value) (string, error) {
	secret, err := scriptJWTSecret(secretValue)
	if err != nil {
		return "", err
	}
	options := scriptJWTOptionsFromValue(optionsValue)
	claims, err := scriptJWTClaims(payloadValue)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if _, ok := claims["iat"]; !ok && !options.NoTimestamp {
		claims["iat"] = float64(now.Unix())
	}
	if options.ExpiresIn != 0 {
		claims["exp"] = float64(now.Add(options.ExpiresIn).Unix())
	}
	if options.NotBefore != 0 {
		claims["nbf"] = float64(now.Add(options.NotBefore).Unix())
	}
	if options.Issuer != "" {
		claims["iss"] = options.Issuer
	}
	if options.Subject != "" {
		claims["sub"] = options.Subject
	}
	if options.Audience != nil {
		claims["aud"] = options.Audience
	}
	method, err := scriptJWTSigningMethod(options.Algorithm)
	if err != nil {
		return "", err
	}
	token := jwtlib.NewWithClaims(method, jwtlib.MapClaims(claims))
	return token.SignedString([]byte(secret))
}

func scriptJWTVerify(tokenValue, secretValue, optionsValue goja.Value) (map[string]interface{}, error) {
	secret, err := scriptJWTSecret(secretValue)
	if err != nil {
		return nil, err
	}
	tokenText := tokenValue.String()
	if strings.Count(tokenText, ".") != 2 {
		return nil, errors.New("jwt malformed")
	}
	options := scriptJWTOptionsFromValue(optionsValue)
	parserOptions := []jwtlib.ParserOption{jwtlib.WithValidMethods(options.Algorithms)}
	if options.IgnoreExpiration {
		parserOptions = append(parserOptions, jwtlib.WithoutClaimsValidation())
	}
	parser := jwtlib.NewParser(parserOptions...)
	claims := jwtlib.MapClaims{}
	token, err := parser.ParseWithClaims(tokenText, claims, func(token *jwtlib.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwtlib.SigningMethodHMAC); !ok {
			return nil, errors.New("invalid algorithm")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, scriptJWTError(err)
	}
	if token == nil || !token.Valid {
		return nil, errors.New("invalid token")
	}
	normalized, err := normalizeJSONValue(map[string]interface{}(claims))
	if err != nil {
		return nil, err
	}
	result, _ := normalized.(map[string]interface{})
	return result, nil
}

func scriptJWTDecode(tokenValue, optionsValue goja.Value) (interface{}, error) {
	tokenText := tokenValue.String()
	if strings.Count(tokenText, ".") != 2 {
		return nil, errors.New("jwt malformed")
	}
	token, _, err := jwtlib.NewParser().ParseUnverified(tokenText, jwtlib.MapClaims{})
	if err != nil {
		return nil, scriptJWTError(err)
	}
	claims, _ := token.Claims.(jwtlib.MapClaims)
	normalizedClaims, err := normalizeJSONValue(map[string]interface{}(claims))
	if err != nil {
		return nil, err
	}
	options := scriptJWTOptionsFromValue(optionsValue)
	if options.Complete {
		header, err := normalizeJSONValue(token.Header)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"header": header, "payload": normalizedClaims, "signature": strings.Split(tokenText, ".")[2]}, nil
	}
	return normalizedClaims, nil
}

type scriptJWTOptions struct {
	Algorithm        string
	Algorithms       []string
	ExpiresIn        time.Duration
	NotBefore        time.Duration
	Issuer           string
	Subject          string
	Audience         interface{}
	IgnoreExpiration bool
	NoTimestamp      bool
	Complete         bool
}

func scriptJWTOptionsFromValue(value goja.Value) scriptJWTOptions {
	options := scriptJWTOptions{Algorithm: "HS256", Algorithms: []string{"HS256", "HS384", "HS512"}}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) || value.Export() == nil {
		return options
	}
	exported, ok := value.Export().(map[string]interface{})
	if !ok {
		return options
	}
	if algorithm, ok := exported["algorithm"].(string); ok && strings.TrimSpace(algorithm) != "" {
		options.Algorithm = algorithm
	}
	if algorithms, ok := exported["algorithms"].([]interface{}); ok && len(algorithms) > 0 {
		options.Algorithms = make([]string, 0, len(algorithms))
		for _, algorithm := range algorithms {
			options.Algorithms = append(options.Algorithms, fmt.Sprint(algorithm))
		}
	}
	if expiresIn, ok := exported["expiresIn"]; ok {
		options.ExpiresIn = parseScriptJWTDuration(expiresIn)
	}
	if notBefore, ok := exported["notBefore"]; ok {
		options.NotBefore = parseScriptJWTDuration(notBefore)
	}
	if issuer, ok := exported["issuer"].(string); ok {
		options.Issuer = issuer
	}
	if subject, ok := exported["subject"].(string); ok {
		options.Subject = subject
	}
	if audience, ok := exported["audience"]; ok {
		options.Audience = audience
	}
	if ignoreExpiration, ok := exported["ignoreExpiration"].(bool); ok {
		options.IgnoreExpiration = ignoreExpiration
	}
	if noTimestamp, ok := exported["noTimestamp"].(bool); ok {
		options.NoTimestamp = noTimestamp
	}
	if complete, ok := exported["complete"].(bool); ok {
		options.Complete = complete
	}
	return options
}

func scriptJWTSecret(value goja.Value) (string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) || strings.TrimSpace(value.String()) == "" {
		return "", errors.New("secret or public key must be provided")
	}
	return value.String(), nil
}

func scriptJWTClaims(value goja.Value) (map[string]interface{}, error) {
	exported := value.Export()
	if exported == nil {
		return map[string]interface{}{}, nil
	}
	normalized, err := normalizeJSONValue(exported)
	if err != nil {
		return nil, err
	}
	switch typed := normalized.(type) {
	case map[string]interface{}:
		return typed, nil
	case string:
		return map[string]interface{}{"data": typed}, nil
	default:
		return nil, errors.New("payload must be an object")
	}
}

func scriptJWTSigningMethod(algorithm string) (*jwtlib.SigningMethodHMAC, error) {
	switch strings.ToUpper(scalar.FirstNonEmpty(algorithm, "HS256")) {
	case "HS256":
		return jwtlib.SigningMethodHS256, nil
	case "HS384":
		return jwtlib.SigningMethodHS384, nil
	case "HS512":
		return jwtlib.SigningMethodHS512, nil
	default:
		return nil, fmt.Errorf("algorithm %s is not supported", algorithm)
	}
}

func parseScriptJWTDuration(value interface{}) time.Duration {
	switch typed := value.(type) {
	case int:
		return time.Duration(typed) * time.Second
	case int64:
		return time.Duration(typed) * time.Second
	case float64:
		return time.Duration(typed) * time.Second
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return 0
		}
		if seconds, err := strconv.ParseFloat(text, 64); err == nil {
			return time.Duration(seconds) * time.Second
		}
		unit := text[len(text)-1]
		amount, err := strconv.ParseFloat(strings.TrimSpace(text[:len(text)-1]), 64)
		if err != nil {
			return 0
		}
		switch unit {
		case 's', 'S':
			return time.Duration(amount * float64(time.Second))
		case 'm', 'M':
			return time.Duration(amount * float64(time.Minute))
		case 'h', 'H':
			return time.Duration(amount * float64(time.Hour))
		case 'd', 'D':
			return time.Duration(amount * float64(24*time.Hour))
		}
	}
	return 0
}

func scriptJWTError(err error) error {
	switch {
	case errors.Is(err, jwtlib.ErrTokenMalformed):
		return errors.New("jwt malformed")
	case errors.Is(err, jwtlib.ErrTokenSignatureInvalid):
		if strings.Contains(strings.ToLower(err.Error()), "signing method") {
			return errors.New("invalid algorithm")
		}
		return errors.New("invalid signature")
	case errors.Is(err, jwtlib.ErrTokenExpired):
		return errors.New("jwt expired")
	case errors.Is(err, jwtlib.ErrTokenNotValidYet):
		return errors.New("jwt not active")
	default:
		return err
	}
}
