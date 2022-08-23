package util

import (
	"github.com/filecoin-project/bacalhau/pkg/job"
	"github.com/filecoin-project/bacalhau/pkg/publisher"
	"github.com/filecoin-project/bacalhau/pkg/publisher/ipfs"
	"github.com/filecoin-project/bacalhau/pkg/publisher/noop"
	"github.com/filecoin-project/bacalhau/pkg/system"
)

func NewIPFSPublishers(
	cm *system.CleanupManager,
	resolver *job.StateResolver,
	ipfsMultiAddress string,
) (map[publisher.PublisherType]publisher.Publisher, error) {
	noopPublisher, err := noop.NewNoopPublisher(cm, resolver)
	if err != nil {
		return nil, err
	}

	ipfsPublisher, err := ipfs.NewIPFSPublisher(cm, resolver, ipfsMultiAddress)
	if err != nil {
		return nil, err
	}

	return map[publisher.PublisherType]publisher.Publisher{
		publisher.PublisherNoop: noopPublisher,
		publisher.PublisherIpfs: ipfsPublisher,
	}, nil
}

func NewNoopPublishers(
	cm *system.CleanupManager,
	resolver *job.StateResolver,
) (map[publisher.PublisherType]publisher.Publisher, error) {
	noopPublisher, err := noop.NewNoopPublisher(cm, resolver)
	if err != nil {
		return nil, err
	}

	return map[publisher.PublisherType]publisher.Publisher{
		publisher.PublisherNoop: noopPublisher,
		publisher.PublisherIpfs: noopPublisher,
	}, nil
}