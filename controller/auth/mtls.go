package auth

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/rpcserver"
)

var testMode = false

func SetTestMode(tm bool) {
	testMode = tm
}

// mtlsKey is unexported: client headers can never carry it, unlike a header.
type mtlsKey struct{}

func IsMTlsConnection(ctx context.Context) (context.Context, bool) {
	if _, alreadyAccepted := ctx.Value(mtlsKey{}).(struct{}); alreadyAccepted {
		return ctx, true
	}

	if testMode {
		username := fmt.Sprintf("%s-%s", realIpFromContext(ctx), "test")
		return acceptAgent(ctx, username, nil), true
	}

	client, ok := rpcserver.PeerFromContext(ctx)
	if !ok || !client.HasClientCertificate() {
		return ctx, false
	}
	cName := client.TLS.PeerCertificates[0].Subject.CommonName
	username := fmt.Sprintf("%s-%s", realIpFromContext(ctx), cName)
	return acceptAgent(ctx, username, []string{fmt.Sprintf("agent-%s", cName)}), true
}

func acceptAgent(ctx context.Context, username string, groups []string) context.Context {
	ctx = context.WithValue(ctx, mtlsKey{}, struct{}{})
	return contexes.WithUserInfo(ctx, username, groups)
}

func realIpFromContext(ctx context.Context) string {
	client, _ := rpcserver.PeerFromContext(ctx)
	callInfo, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return client.Addr
	}
	header := callInfo.RequestHeader()
	if realIp := header.Get("X-Real-IP"); realIp != "" {
		return realIp
	}
	if forwardedFor := header.Get("X-Forwarded-For"); forwardedFor != "" {
		return strings.Split(forwardedFor, ", ")[0]
	}
	return client.Addr
}

// ClientCommonName gives the common name of the client certificate of the call. Test mode has no bypass here because
// the common name is the identity of an agent.
func ClientCommonName(ctx context.Context) (string, bool) {
	client, ok := rpcserver.PeerFromContext(ctx)
	if !ok || !client.HasClientCertificate() {
		return "", false
	}
	commonName := client.TLS.PeerCertificates[0].Subject.CommonName
	return commonName, commonName != ""
}
