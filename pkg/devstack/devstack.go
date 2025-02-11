package devstack

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/filecoin-project/bacalhau/pkg/logger"
	filecoinlotus "github.com/filecoin-project/bacalhau/pkg/publisher/filecoin_lotus"

	"github.com/filecoin-project/bacalhau/pkg/localdb"
	"github.com/filecoin-project/bacalhau/pkg/localdb/inmemory"
	"github.com/filecoin-project/bacalhau/pkg/transport"

	"github.com/filecoin-project/bacalhau/pkg/config"
	"github.com/filecoin-project/bacalhau/pkg/ipfs"
	"github.com/filecoin-project/bacalhau/pkg/model"
	"github.com/filecoin-project/bacalhau/pkg/node"
	"github.com/filecoin-project/bacalhau/pkg/requesternode"
	"github.com/filecoin-project/bacalhau/pkg/system"
	"github.com/filecoin-project/bacalhau/pkg/transport/libp2p"
	"github.com/filecoin-project/bacalhau/pkg/transport/simulator"
	"github.com/multiformats/go-multiaddr"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"
)

type DevStackOptions struct {
	NumberOfNodes        int    // Number of nodes to start in the cluster
	NumberOfBadActors    int    // Number of nodes to be bad actors
	Peer                 string // Connect node 0 to another network node
	PublicIPFSMode       bool   // Use public IPFS nodes
	LocalNetworkLotus    bool
	FilecoinUnsealedPath string
	EstuaryAPIKey        string
	SimulatorURL         string // if this is set, we will use the simulator transport
}
type DevStack struct {
	Nodes []*node.Node
	Lotus *LotusNode
}

func NewDevStackForRunLocal(
	ctx context.Context,
	cm *system.CleanupManager,
	count int,
	jobGPU uint64, //nolint:unparam // Incorrectly assumed as unused
) (*DevStack, error) {
	options := DevStackOptions{
		NumberOfNodes:  count,
		PublicIPFSMode: true,
	}

	computeConfig := node.NewComputeConfigWith(node.ComputeConfigParams{
		TotalResourceLimits: model.ResourceUsageData{GPU: jobGPU},
		JobSelectionPolicy: model.JobSelectionPolicy{
			Locality:            model.Anywhere,
			RejectStatelessJobs: false,
		},
	})

	return NewStandardDevStack(
		ctx,
		cm,
		options,
		computeConfig,
		requesternode.NewDefaultRequesterNodeConfig(),
	)
}

func NewStandardDevStack(
	ctx context.Context,
	cm *system.CleanupManager,
	options DevStackOptions,
	computeConfig node.ComputeConfig,
	requesterNodeConfig requesternode.RequesterNodeConfig,
) (*DevStack, error) {
	return NewDevStack(ctx, cm, options, computeConfig, requesterNodeConfig, node.NewStandardNodeDependencyInjector())
}

func NewNoopDevStack(
	ctx context.Context,
	cm *system.CleanupManager,
	options DevStackOptions,
	computeConfig node.ComputeConfig,
	requesterNodeConfig requesternode.RequesterNodeConfig,
) (*DevStack, error) {
	return NewDevStack(ctx, cm, options, computeConfig, requesterNodeConfig, NewNoopNodeDependencyInjector())
}

//nolint:funlen,gocyclo
func NewDevStack(
	ctx context.Context,
	cm *system.CleanupManager,
	options DevStackOptions,
	computeConfig node.ComputeConfig,
	requesterNodeConfig requesternode.RequesterNodeConfig,
	injector node.NodeDependencyInjector,
) (*DevStack, error) {
	ctx, span := system.GetTracer().Start(ctx, "pkg/devstack.newdevstack")
	defer span.End()

	nodes := []*node.Node{}
	var lotus *LotusNode
	var err error

	if options.LocalNetworkLotus {
		lotus, err = newLotusNode(ctx) //nolint:govet
		if err != nil {
			return nil, err
		}

		cm.RegisterCallback(lotus.Close)

		if err := lotus.start(ctx); err != nil { //nolint:govet
			return nil, err
		}
	}

	for i := 0; i < options.NumberOfNodes; i++ {
		log.Debug().Msgf(`Creating Node #%d`, i)

		// -------------------------------------
		// IPFS
		// -------------------------------------
		var ipfsNode *ipfs.Node
		var ipfsClient *ipfs.Client

		var ipfsSwarmAddrs []string
		if i > 0 {
			ipfsSwarmAddrs, err = nodes[0].IPFSClient.SwarmAddresses(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get ipfs swarm addresses: %w", err)
			}
		}

		ipfsNode, err = createIPFSNode(ctx, cm, options.PublicIPFSMode, ipfsSwarmAddrs)
		if err != nil {
			return nil, fmt.Errorf("failed to create ipfs node: %w", err)
		}

		ipfsClient, err = ipfsNode.Client()
		if err != nil {
			return nil, fmt.Errorf("failed to create ipfs client: %w", err)
		}

		// Assign all the ports up front, so that they can't collide
		var ports []int
		ports, err = freeport.GetFreePorts(3)
		if err != nil {
			return nil, err
		}

		var useTransport transport.Transport

		var libp2pPort int
		if options.SimulatorURL == "" {
			//////////////////////////////////////
			// libp2p
			//////////////////////////////////////

			libp2pPort, ports = ports[0], ports[1:]
			libp2pPeer := []multiaddr.Multiaddr{}

			if i == 0 {
				if options.Peer != "" {
					// connect 0'th node to external peer if specified
					log.Debug().Msgf("Connecting 0'th node to remote peer: %s", options.Peer)
					peerAddr, addrErr := multiaddr.NewMultiaddr(options.Peer)
					if addrErr != nil {
						return nil, fmt.Errorf("failed to parse peer address: %w", addrErr)
					}
					libp2pPeer = []multiaddr.Multiaddr{peerAddr}
				}
			} else {
				libp2pPeer, err = nodes[0].Transport.HostAddrs()
				if err != nil {
					return nil, fmt.Errorf("failed to get libp2p addresses: %w", err)
				}
				log.Debug().Msgf("Connecting to first libp2p scheduler node: %s", libp2pPeer)
			}

			libp2pTransport, transportErr := libp2p.NewTransport(ctx, cm, libp2pPort, libp2pPeer)
			if transportErr != nil {
				return nil, transportErr
			}

			useTransport = libp2pTransport
		} else {
			var simulatorTransport transport.Transport
			simulatorTransport, err = simulator.NewTransport(ctx, cm, fmt.Sprintf("simulator-node-%d", i), options.SimulatorURL)
			if err != nil {
				return nil, err
			}
			useTransport = simulatorTransport
		}

		// add NodeID to logging context
		ctx = logger.ContextWithNodeIDLogger(ctx, useTransport.HostID())

		//////////////////////////////////////
		// port for API
		//////////////////////////////////////
		var apiPort int
		if os.Getenv("PREDICTABLE_API_PORT") != "" {
			apiPort = 20000 + i
		} else {
			apiPort, ports = ports[0], ports[1:]
		}

		//////////////////////////////////////
		// metrics
		//////////////////////////////////////
		var metricsPort int
		metricsPort, _ = ports[0], ports[1:]

		//////////////////////////////////////
		// in-memory datastore
		//////////////////////////////////////
		var datastore localdb.LocalDB
		datastore, err = inmemory.NewInMemoryDatastore()
		if err != nil {
			return nil, err
		}

		//////////////////////////////////////
		// Create and Run Node
		//////////////////////////////////////
		isBadActor := (options.NumberOfBadActors > 0) && (i >= options.NumberOfNodes-options.NumberOfBadActors)

		nodeConfig := node.NodeConfig{
			IPFSClient:           ipfsClient,
			CleanupManager:       cm,
			LocalDB:              datastore,
			Transport:            useTransport,
			FilecoinUnsealedPath: options.FilecoinUnsealedPath,
			EstuaryAPIKey:        options.EstuaryAPIKey,
			HostAddress:          "0.0.0.0",
			HostID:               useTransport.HostID(),
			APIPort:              apiPort,
			MetricsPort:          metricsPort,
			ComputeConfig:        computeConfig,
			RequesterNodeConfig:  requesterNodeConfig,
			IsBadActor:           isBadActor,
		}

		if lotus != nil {
			nodeConfig.LotusConfig = &filecoinlotus.PublisherConfig{
				StorageDuration: 24 * 24 * time.Hour,
				PathDir:         lotus.PathDir,
				UploadDir:       lotus.UploadDir,
				// devstack will only be talking to a single node, so don't bother filtering based on ping
				// as the ping may be quite large while it is trying to run everything
				MaximumPing: time.Duration(math.MaxInt64),
			}
		}

		var n *node.Node
		n, err = node.NewNode(ctx, nodeConfig, injector)
		if err != nil {
			return nil, err
		}

		// Start transport layer
		err = useTransport.Start(ctx)
		if err != nil {
			return nil, err
		}

		// start the node
		err = n.Start(ctx)
		if err != nil {
			return nil, err
		}

		nodes = append(nodes, n)

		// let's wait a small period to give the api server a chance to spin up
		// meaning it's port will be in use the next time we spin around this loop
		// and so hopefully avoid "listen tcp 0.0.0.0:43081: bind: address already in use" errors
		time.Sleep(time.Millisecond * 100) //nolint:gomnd
	}

	// only start profiling after we've set everything up!
	profiler := StartProfiling()
	cm.RegisterCallback(profiler.Close)

	return &DevStack{
		Nodes: nodes,
		Lotus: lotus,
	}, nil
}

func createIPFSNode(ctx context.Context,
	cm *system.CleanupManager,
	publicIPFSMode bool,
	ipfsSwarmAddrs []string) (*ipfs.Node, error) {
	ctx, span := system.GetTracer().Start(ctx, "pkg/devstack.createipfsnode")
	defer span.End()
	//////////////////////////////////////
	// IPFS
	//////////////////////////////////////
	var err error
	var ipfsNode *ipfs.Node

	if publicIPFSMode {
		ipfsNode, err = ipfs.NewNode(ctx, cm, []string{})
		if err != nil {
			return nil, fmt.Errorf("failed to create ipfs node: %w", err)
		}
	} else {
		ipfsNode, err = ipfs.NewLocalNode(ctx, cm, ipfsSwarmAddrs)
		if err != nil {
			return nil, fmt.Errorf("failed to create ipfs node: %w", err)
		}
	}
	return ipfsNode, nil
}

func (stack *DevStack) PrintNodeInfo(ctx context.Context) (string, error) {
	if !config.DevstackGetShouldPrintInfo() {
		return "", nil
	}

	logString := ""
	devStackAPIPort := ""
	devStackAPIHost := "0.0.0.0"
	devStackIPFSSwarmAddress := ""

	logString += `
-----------------------------------------
-----------------------------------------
`
	for nodeIndex, node := range stack.Nodes {
		swarmAddrrs := ""
		swarmAddresses, err := node.IPFSClient.SwarmAddresses(ctx)
		if err != nil {
			return "", fmt.Errorf("cannot get swarm addresses for node %d", nodeIndex)
		} else {
			swarmAddrrs = strings.Join(swarmAddresses, ",")
		}

		logString += fmt.Sprintf(`
export BACALHAU_IPFS_%d=%s
export BACALHAU_IPFS_SWARM_ADDRESSES_%d=%s
export BACALHAU_API_HOST_%d=%s
export BACALHAU_API_PORT_%d=%d`,
			nodeIndex,
			node.IPFSClient.APIAddress(),
			nodeIndex,
			swarmAddrrs,
			nodeIndex,
			stack.Nodes[nodeIndex].APIServer.Host,
			nodeIndex,
			stack.Nodes[nodeIndex].APIServer.Port,
		)

		// Just setting this to the last one, really doesn't matter
		swarmAddressesList, _ := node.IPFSClient.SwarmAddresses(ctx)
		devStackIPFSSwarmAddress = strings.Join(swarmAddressesList, ",")
		devStackAPIHost = stack.Nodes[nodeIndex].APIServer.Host
		devStackAPIPort = fmt.Sprintf("%d", stack.Nodes[nodeIndex].APIServer.Port)
	}

	// Just convenience below - print out the last of the nodes information as the global variable
	summaryShellVariablesString := fmt.Sprintf(`
export BACALHAU_IPFS_SWARM_ADDRESSES=%s
export BACALHAU_API_HOST=%s
export BACALHAU_API_PORT=%s`,
		devStackIPFSSwarmAddress,
		devStackAPIHost,
		devStackAPIPort,
	)

	if stack.Lotus != nil {
		summaryShellVariablesString += fmt.Sprintf(`
export LOTUS_PATH=%s
export LOTUS_UPLOAD_DIR=%s`, stack.Lotus.PathDir, stack.Lotus.UploadDir)
	}

	log.Debug().Msg(logString)

	returnString := fmt.Sprintf(`
Devstack is ready!
To use the devstack, run the following commands in your shell: %s`, summaryShellVariablesString)
	return returnString, nil
}

func (stack *DevStack) GetNode(ctx context.Context, nodeID string) (
	*node.Node, error) {
	for _, node := range stack.Nodes {
		if node.Transport.HostID() == nodeID {
			return node, nil
		}
	}

	return nil, fmt.Errorf("node not found: %s", nodeID)
}
func (stack *DevStack) IPFSClients() []*ipfs.Client {
	clients := make([]*ipfs.Client, 0, len(stack.Nodes))
	for _, node := range stack.Nodes {
		clients = append(clients, node.IPFSClient)
	}
	return clients
}

func (stack *DevStack) GetNodeIds() ([]string, error) {
	var ids []string
	for _, node := range stack.Nodes {
		ids = append(ids, node.Transport.HostID())
	}

	return ids, nil
}
