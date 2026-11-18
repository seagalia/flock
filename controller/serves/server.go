package serves

import (
	"context"
	"crypto/tls"
	"sync"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/announcers"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/controller/permissioners"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/core/v1/svccoreconnect"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	"github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"
)

type FlockServer struct {
	entry              zerolog.Logger
	mtlsConfig         *tls.Config
	admConf            config.AdminConfig
	storeMan           storers.StoreManager
	linker             announcers.Linker
	jwtVerify          *svclogin.JwtManager
	login              auth.AuthLogin
	permissioner       permissioners.Permissioner
	instantiater       instantiaters.Instantiater
	ipam               ipams.Ipam
	agentClients       *helpers.GrpcClients
	listenFactory      func(ctx context.Context, stream DiscoveryStream, done chan *DoneEmit, errEmit chan *ErrEmit) announcers.Listener
	doneChanFactory    func() chan *DoneEmit
	errEmitChanFactory func() chan *ErrEmit
	initDiscoverer     func(ctx context.Context, entry zerolog.Logger,
		stream DiscoveryStream,
		waitInit chan struct{}, done chan *DoneEmit) func(changes []*svcdiscovery.Resource, removes []string)
	ctx      context.Context
	locker   lockers.Locker
	leases   *lockers.Leases
	presence *nodePresence
	// the work a call starts and that outlives its answer, the stop of the app waits for it before closing the store
	background sync.WaitGroup

	svcdiscoveryconnect.UnimplementedAggregatedDiscoveryHandler
	svcloginconnect.UnimplementedLoginHandler
	svccertconnect.UnimplementedCertificateHandler
	svcsecretconnect.UnimplementedSecretHandler
	svcvsconnect.UnimplementedVirtualServerHandler
	svcbackendconnect.UnimplementedBackendHandler
	svcinstanceconnect.UnimplementedInstanceHandler
	svcvipconnect.UnimplementedVIPHandler
	svcpermconnect.UnimplementedPermissionHandler
	svccoreconnect.UnimplementedInfoHandler
	svclookupconnect.UnimplementedLookupHandler
	svczoneconnect.UnimplementedZoneHandler
	svcleaseconnect.UnimplementedLeaseHandler
}

func NewFlockServer(
	ctx context.Context,
	mtlsConfig *tls.Config,
	admConf config.AdminConfig,
	linker announcers.Linker,
	storeMan storers.StoreManager,
	jwtManager *svclogin.JwtManager,
	login auth.AuthLogin,
	permissioner permissioners.Permissioner,
	instantiater instantiaters.Instantiater,
	ipam ipams.Ipam,
	locker lockers.Locker,
	leaseStore *lockers.Leases,
) *FlockServer {
	entry := logging.Component("flock_controller_server")
	return &FlockServer{
		admConf:      admConf,
		mtlsConfig:   mtlsConfig,
		linker:       linker,
		entry:        entry,
		storeMan:     storeMan,
		jwtVerify:    jwtManager,
		login:        login,
		permissioner: permissioner,
		instantiater: instantiater,
		ctx:          ctx,
		agentClients: helpers.NewGrpcClients(mtlsConfig),
		listenFactory: func(ctx context.Context, stream DiscoveryStream, done chan *DoneEmit, errEmit chan *ErrEmit) announcers.Listener {
			return NewDiscoAnList(ctx, stream, done, errEmit)
		},
		doneChanFactory: func() chan *DoneEmit {
			return make(chan *DoneEmit, 1)
		},
		errEmitChanFactory: func() chan *ErrEmit {
			return make(chan *ErrEmit, 1)
		},
		initDiscoverer: DefaultInitDiscoverer,
		ipam:           ipam,
		locker:         locker,
		leases:         leaseStore,
		presence:       newNodePresence(storeMan.Nodes(), nodeRefreshEvery, entry),
	}
}
