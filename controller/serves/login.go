package serves

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/sdk/contexes"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const errReservedUsername = "username " + svclogin.BreakGlassUsername + " is reserved"

var allowedNoLoginService = []string{
	"/flock.services.login.v1.Login",
}

func (s *FlockServer) GetToken(ctx context.Context, request *svclogin.GetTokenRequest) (*svclogin.GetTokenResponse, error) {
	userLogin, isUserLogin := s.login.(auth.AuthUserPassword)
	if !isUserLogin {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "this controller logs in with OIDC, get a token from the provider")
	}
	identity, logged, err := userLogin.LoginUser(request.GetUsername(), request.GetPassword())(ctx)
	if !logged || identity.Username == "" {
		s.logLogin(ctx, "/flock.services.login.v1.Login/GetToken", "AuthFailed", true)
		if err != nil {
			s.entry.Error().Msgf("error during login: %s", err.Error())
		}
		errMesg := "invalid credentials"
		if logged && identity.Username == "" {
			errMesg += ", login info give empty username"
		}

		return nil, connect.NewError(connect.CodeUnauthenticated, errMesg)
	}

	if identity.Username == svclogin.BreakGlassUsername {
		s.logLogin(ctx, "/flock.services.login.v1.Login/GetToken", "AuthFailed", true)
		return nil, connect.Errorf(connect.CodeUnauthenticated, errReservedUsername)
	}

	token, expiresAt, err := s.jwtVerify.MakeToken(identity.Username, identity.Groups)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "error while creating token: %s", err.Error())
	}
	return &svclogin.GetTokenResponse{Token: token, ExpiresAt: timestamppb.New(expiresAt)}, nil
}

func (s *FlockServer) GetServiceAuth(context.Context, *emptypb.Empty) (*svclogin.ServiceAuth, error) {
	oidcLogin, isOidc := s.login.(*auth.OidcLogin)
	if !isOidc {
		return &svclogin.ServiceAuth{Type: s.login.Type()}, nil
	}
	provider := oidcLogin.Provider()
	return &svclogin.ServiceAuth{
		Type: s.login.Type(),
		Oidc: &svclogin.OidcProvider{
			IssuerUrl: provider.IssuerUrl,
			ClientId:  provider.ClientId,
			Scopes:    provider.Scopes,
		},
	}, nil
}

func (s *FlockServer) AuthFuncOverride(ctx context.Context, fullMethodName string) (context.Context, error) {
	if newCtx, ok := auth.IsMTlsConnection(ctx); ok {
		ctx = newCtx
		if username, _, _ := contexes.RetrieveUserInfo(ctx); username == svclogin.BreakGlassUsername {
			s.logLogin(ctx, fullMethodName, "Denied "+errReservedUsername, true)
			return nil, connect.NewError(connect.CodeUnauthenticated, errReservedUsername)
		}
		s.logLogin(ctx, fullMethodName, "Allowed", false)
		return ctx, nil
	}

	for _, nls := range allowedNoLoginService {
		if !strings.HasPrefix(fullMethodName, nls) {
			continue
		}
		s.logLogin(ctx, fullMethodName, "Allowed", false)
		return ctx, nil
	}

	identity, isBreakGlass, err := s.verifyBearer(ctx, fullMethodName)
	if err != nil {
		s.logLogin(ctx, fullMethodName, "Denied "+err.Error(), true)
		return nil, err
	}
	ctx = contexes.WithUserInfo(ctx, identity.Username, identity.Groups)
	if isBreakGlass {
		ctx = contexes.WithBreakGlass(ctx)
	}
	s.logLogin(ctx, fullMethodName, "Allowed", false)
	return ctx, nil
}

// verifyBearer checks the token of the authorization header with the verifier of its issuer: the jwt_maker key for
// flock tokens, the OIDC provider for its own tokens
func (s *FlockServer) verifyBearer(ctx context.Context, fullMethodName string) (auth.Identity, bool, error) {
	token, err := bearerToken(ctx)
	if err != nil {
		return auth.Identity{}, false, connect.NewError(connect.CodeUnauthenticated, err.Error())
	}
	// the issuer only picks the verifier, which then checks the signature and the claims
	unverified, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return auth.Identity{}, false, connect.Errorf(connect.CodeUnauthenticated, "invalid token: %s", err.Error())
	}
	issuer, err := unverified.Claims.GetIssuer()
	if err != nil || issuer == "" {
		return auth.Identity{}, false, connect.Errorf(connect.CodeUnauthenticated, "invalid token: no issuer")
	}

	if issuer == s.jwtVerify.Issuer() {
		username, groups, err := s.jwtVerify.VerifyToken(token)
		if err != nil {
			return auth.Identity{}, false, connect.Errorf(connect.CodeUnauthenticated, "invalid token: %s", err.Error())
		}
		return s.flockTokenIdentity(ctx, fullMethodName, token, username, groups)
	}

	verifier, isVerifier := s.login.(auth.TokenVerifier)
	isProviderToken := isVerifier && issuer == verifier.Issuer()
	if !isProviderToken {
		return auth.Identity{}, false, connect.Errorf(connect.CodeUnauthenticated, "unknown token issuer %s", issuer)
	}
	identity, err := verifier.VerifyToken(ctx, token)
	if errors.Is(err, auth.ErrProviderUnavailable) {
		return auth.Identity{}, false, connect.NewError(connect.CodeUnavailable, err.Error())
	}
	if err != nil {
		return auth.Identity{}, false, connect.Errorf(connect.CodeUnauthenticated, "invalid token: %s", err.Error())
	}
	if identity.Username == svclogin.BreakGlassUsername {
		return auth.Identity{}, false, connect.NewError(connect.CodeUnauthenticated, errReservedUsername)
	}
	return identity, false, nil
}

// flockTokenIdentity gives the identity of a verified flock token. Only a valid break-glass token may carry the
// reserved username, and every call made with one is logged as it bypasses the login provider. The bool tells a
// break-glass token.
func (s *FlockServer) flockTokenIdentity(ctx context.Context, fullMethodName string, token string, username string, groups []string) (auth.Identity, bool, error) {
	breakGlass, err := s.jwtVerify.BreakGlassClaims(token)
	if err != nil {
		return auth.Identity{}, false, connect.Errorf(connect.CodeUnauthenticated, "invalid token: %s", err.Error())
	}
	isReservedUsername := username == svclogin.BreakGlassUsername
	if breakGlass == nil && isReservedUsername {
		return auth.Identity{}, false, connect.NewError(connect.CodeUnauthenticated, errReservedUsername)
	}
	if breakGlass != nil && !isReservedUsername {
		return auth.Identity{}, false, connect.NewError(connect.CodeUnauthenticated, "invalid token: break-glass token for another user")
	}
	if breakGlass == nil {
		return auth.Identity{Username: username, Groups: groups}, false, nil
	}
	s.entry.Warn().
		Str("break_glass_id", breakGlass.ID).
		Str("reason", breakGlass.Reason).
		Time("expires_at", breakGlass.ExpiresAt).
		Str("method", fullMethodName).
		Str("client_ip", clientAddr(ctx)).
		Msg("break-glass access")
	return auth.Identity{Username: username, Groups: groups}, true, nil
}

// bearerToken gives the token of the authorization header, the scheme is case-insensitive
func bearerToken(ctx context.Context) (string, error) {
	authorization, err := contexes.RetrieveAuthorization(ctx)
	if err != nil {
		return "", fmt.Errorf("missing authorization header")
	}
	scheme, token, found := strings.Cut(authorization, " ")
	isBearer := found && strings.EqualFold(scheme, "bearer") && strings.TrimSpace(token) != ""
	if !isBearer {
		return "", fmt.Errorf("authorization header format must be Bearer {token}")
	}
	return strings.TrimSpace(token), nil
}

func (s *FlockServer) Me(ctxMe context.Context, _ *emptypb.Empty) (*svclogin.MeResponse, error) {
	ctx, err := s.AuthFuncOverride(ctxMe, "flock.services.login.v1.Login/Me")
	if err != nil {
		return nil, err
	}
	username, groups, _ := contexes.RetrieveUserInfo(ctx)
	return &svclogin.MeResponse{
		Username: username,
		Groups:   groups,
		IsAdmin:  s.userIsAdmin(ctx, username, groups),
	}, nil
}

func (s *FlockServer) logLogin(ctx context.Context, fullMethodName string, prefix string, warn bool) {
	fields := make(map[string]any)
	if clientIp := clientAddr(ctx); clientIp != "" {
		fields["client_ip"] = clientIp
	}
	username, groups, ok := contexes.RetrieveUserInfo(ctx)
	if ok {
		fields["username"] = username
		fields["groups"] = groups
	}
	fields["server"] = "grpc"
	fields["method"] = "login"
	level := zerolog.InfoLevel
	if warn {
		level = zerolog.WarnLevel
	}
	s.entry.WithLevel(level).Fields(fields).Msgf("%s -> %s", prefix, fullMethodName)
}
