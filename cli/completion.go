package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/posener/complete"
	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/willabides/kongplete"
)

const defaultCompletionTimeout = 2 * time.Second

// completionTimeout bounds one completion, a variable so specs can shorten it.
var completionTimeout = defaultCompletionTimeout

// completionContext gives the ctx of one completion. The shell runs the completion as its own short process, so the
// context is rooted here and the deadline keeps a stalled controller from hanging the user's prompt.
func completionContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), completionTimeout)
}

// completionConnect opens the connection used to complete resource names,
// tests replace it to complete from a fake server.
var completionConnect = CompletionClient

// CompletionPredictors are referenced by the predictor tags of flags and arguments.
func CompletionPredictors() []kongplete.Option {
	return []kongplete.Option{
		kongplete.WithPredictor("file", complete.PredictFiles("*")),
		kongplete.WithPredictor("instance", predictResource(helpers.TypeUrl[*instance.Instance]())),
		kongplete.WithPredictor("backend", predictResource(helpers.TypeUrl[*backend.Backend]())),
		kongplete.WithPredictor("vip", predictResource(helpers.TypeUrl[*vip.VIP]())),
		kongplete.WithPredictor("vs", predictResource(helpers.TypeUrl[*vs.VirtualServer]())),
		kongplete.WithPredictor("certificate", predictResource(helpers.TypeUrl[*cert.Certificate]())),
		kongplete.WithPredictor("secret", predictResource(helpers.TypeUrl[*secret.Secret]())),
		kongplete.WithPredictor("zone", predictResource(helpers.TypeUrl[*zone.Zone]())),
		kongplete.WithPredictor("zone-pool", complete.PredictFunc(predictZonePool)),
		kongplete.WithPredictor("by-resource-type", complete.PredictFunc(predictByResourceType)),
	}
}

func predictResource(typeUrl string) complete.Predictor {
	return complete.PredictFunc(func(args complete.Args) []string {
		ctx, cancel := completionContext()
		defer cancel()
		return completeResource(ctx, args.Last, typeUrl)
	})
}

// predictByResourceType completes the resource name of commands where -r/--resource-type gives its type.
func predictByResourceType(args complete.Args) []string {
	for i, arg := range args.All {
		isTypeFlag := arg == "-r" || arg == "--resource-type"
		if !isTypeFlag || i+1 >= len(args.All) {
			continue
		}
		typeUrl, ok := enumType[args.All[i+1]]
		if !ok {
			return nil
		}
		ctx, cancel := completionContext()
		defer cancel()
		return completeResource(ctx, args.Last, typeUrl)
	}
	return nil
}

// predictZonePool completes the pool name of delete-pool-from-zone, the zone is the argument before it.
func predictZonePool(args complete.Args) []string {
	zoneIdentifier := ""
	for i, arg := range args.All {
		isZoneBeforePool := arg == "delete-pool-from-zone" && i+2 < len(args.All)
		if isZoneBeforePool {
			zoneIdentifier = args.All[i+1]
			break
		}
	}
	if zoneIdentifier == "" {
		return nil
	}

	ctx, cancel := completionContext()
	defer cancel()
	client, err := completionClientOrError()
	if err != nil {
		return []string{"Error: " + err.Error()}
	}
	zoneNamed := ZoneNamed{NameUuid{identifier: zoneIdentifier}}
	aZone, err := FindZone(ctx, client, &zoneNamed)
	if err != nil {
		return completionFailure(err)
	}

	names := make([]string, 0)
	for _, pool := range aZone.GetPools() {
		if strings.HasPrefix(pool.GetName(), args.Last) {
			names = append(names, pool.GetName())
		}
	}
	return names
}

func completeResource(ctx context.Context, match string, typeUrl string) []string {
	client, err := completionClientOrError()
	if err != nil {
		return []string{"Error: " + err.Error()}
	}

	elems, err := FindTypedByName(ctx, client, typeUrl, match, svccore.ListFilter_LIKE)
	if err != nil {
		return completionFailure(err)
	}
	names := make([]string, 0)
	for _, elem := range elems {
		names = append(names, helpers.GetName(elem))
	}
	return names
}

// completionFailure shows the error as the only proposal. A controller too slow to answer gets no proposal at all,
// because a timeout text inserted in the user's command line would be noise.
func completionFailure(err error) []string {
	isTooSlow := errors.Is(err, context.DeadlineExceeded) || connect.CodeOf(err) == connect.CodeDeadlineExceeded
	if isTooSlow {
		return nil
	}
	return []string{"Error: " + err.Error()}
}

func completionClientOrError() (*clients.Client, error) {
	client, err := completionConnect()
	if err != nil {
		return nil, err
	}
	return clients.New(client), nil
}

// CompletionClient connects with the saved config: completion runs before flags are parsed.
func CompletionClient() (*connect.Client, error) {
	configPath = defaultConfigPath
	if os.Getenv("FLOCK_CLI_CONFIG") != "" {
		configPath = os.Getenv("FLOCK_CLI_CONFIG")
	}

	return app.CompletionClient(ExpandConfigPath())
}
