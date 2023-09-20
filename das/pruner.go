package das

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/celestiaorg/celestia-node/header"
	"github.com/celestiaorg/celestia-node/share"
	"github.com/celestiaorg/celestia-node/share/eds"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/ipfs/go-datastore/query"
)

const (
	dsPrefix = "/pruner/epoch/"
)

var (
	EpochDuration = time.Minute
)

type StoragePruner struct {
	cancel context.CancelFunc

	// TODO: Race?
	oldestEpoch uint64

	stripedLocks [256]sync.Mutex
	activeEpochs map[uint64]struct{}

	ds    datastore.Batching
	store *eds.Store

	done chan struct{}
}

func NewStoragePruner(ds datastore.Batching, store *eds.Store) *StoragePruner {
	return &StoragePruner{
		// set to max uint64 as sentinel before state is restored or first epoch is registered
		oldestEpoch:  ^uint64(0),
		activeEpochs: make(map[uint64]struct{}),
		ds:           namespace.Wrap(ds, datastore.NewKey(dsPrefix)),
		store:        store,
		done:         make(chan struct{}),
	}
}

func (sp *StoragePruner) Start(ctx context.Context) error {
	sp.restoreState(ctx)
	ctx, cancel := context.WithCancel(context.Background())
	sp.cancel = cancel

	go sp.run(ctx)
	return nil
}

func (sp *StoragePruner) Stop(ctx context.Context) error {
	sp.cancel()
	<-sp.done
	return nil
}

func (sp *StoragePruner) restoreState(ctx context.Context) error {
	results, err := sp.ds.Query(ctx, query.Query{})
	if err != nil {
		return fmt.Errorf("failed to recover pruner state from datastore: %w", err)
	}
	for {
		res, ok := results.NextSync()
		if !ok {
			break
		}
		epoch, err := strconv.ParseUint(res.Key[len(dsPrefix):], 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse epoch from datastore: %w", err)
		}
		// we don't need to use locks here because no methods will be called until the callers have also started
		sp.activeEpochs[epoch] = struct{}{}
		if epoch < sp.oldestEpoch {
			sp.oldestEpoch = epoch
		}
	}
	log.Infow("pruner: restored state from datastore", "oldestEpoch", sp.oldestEpoch, "active epoch count", len(sp.activeEpochs))
	return nil
}

func (sp *StoragePruner) Register(ctx context.Context, h *header.ExtendedHeader) error {
	var datahashes []share.DataHash
	var err error

	if share.DataHash(h.DAH.Hash()).IsEmptyRoot() {
		return nil
	}

	epoch := calculateEpoch(h.Time())
	lk := &sp.stripedLocks[epoch%256]
	lk.Lock()
	defer lk.Unlock()
	log.Infof("pruner: registering datahash %X to epoch %d", h.DAH.Hash(), epoch)
	_, ok := sp.activeEpochs[epoch]
	if ok { // epoch already registered, load existing datahashes from datastore
		datahashes, err = sp.getDatahashesFromEpoch(ctx, epoch)
		if err != nil {
			return err
		}
	} else { // epoch not already registered
		log.Infow("pruner: registering new epoch", "epoch", epoch)
		sp.activeEpochs[epoch] = struct{}{}
	}

	datahashes = append(datahashes, h.DAH.Hash())
	return sp.saveDatahashesToEpoch(ctx, epoch, datahashes)
}

func (sp *StoragePruner) gc(ctx context.Context) error {
	for epoch := range sp.activeEpochs {
		err := sp.pruneEpoch(ctx, epoch)
		if err != nil {
			return err
		}
	}
	return nil
}

func (sp *StoragePruner) run(ctx context.Context) error {
	ticker := time.NewTicker(EpochDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sp.pruneEpoch(ctx, sp.oldestEpoch)
			// skip gc if there is nothing to collect after pruning oldest epoch
			if len(sp.activeEpochs) < int(float64(recencyWindow)/float64(EpochDuration)) {
				continue
			}

			err := sp.gc(ctx)
			if err != nil {
				log.Errorw("failed to gc pruner", "err", err)
			}
		case <-ctx.Done():
			sp.done <- struct{}{}
			return nil
		}
	}
}

func (sp *StoragePruner) pruneEpoch(ctx context.Context, epoch uint64) error {
	if epochIsRecent(epoch) {
		return nil
	}

	log.Infow("pruner: pruning epoch", "epoch", epoch)
	if epoch == sp.oldestEpoch {
		sp.oldestEpoch = ^uint64(0)
		sp.updateOldestEpoch()
	}

	lk := &sp.stripedLocks[epoch%256]
	lk.Lock()
	defer lk.Unlock()
	datahashes, err := sp.getDatahashesFromEpoch(ctx, epoch)
	if err != nil {
		return err
	}

	for _, dh := range datahashes {
		err = sp.store.Remove(ctx, dh)
		if err != nil {
			return err
		}
	}

	delete(sp.activeEpochs, epoch)
	return nil
}

func (sp *StoragePruner) updateOldestEpoch() {
	// TODO: This is obviously not ideal and we should track the oldest epoch in a more efficient way instead
	// or just calculate it based off of time offsets and make sure we cover any edge cases
	for key := range sp.activeEpochs {
		if key < sp.oldestEpoch {
			sp.oldestEpoch = key
		}
	}
}

func epochIsRecent(epoch uint64) bool {
	return epoch >= calculateEpoch(time.Now().Add(-recencyWindow))
}

func (sp *StoragePruner) getDatahashesFromEpoch(ctx context.Context, epoch uint64) ([]share.DataHash, error) {
	var datahashes []share.DataHash
	key := datastore.NewKey(fmt.Sprintf("%d", epoch))
	val, err := sp.ds.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get datahashes for epoch %d: %w", epoch, err)
	}
	if err := json.Unmarshal(val, &datahashes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal datahashes for epoch %d: %w", epoch, err)
	}
	return datahashes, nil
}

func (sp *StoragePruner) saveDatahashesToEpoch(ctx context.Context, epoch uint64, datahashes []share.DataHash) error {
	key := datastore.NewKey(fmt.Sprintf("%d", epoch))
	// TODO: Can we avoid expensive JSON marshal/unmarshal
	bz, err := json.Marshal(datahashes)
	if err != nil {
		return fmt.Errorf("failed to marshal datahashes for epoch %d: %w", epoch, err)
	}
	err = sp.ds.Put(ctx, key, bz)
	if err != nil {
		return fmt.Errorf("failed to put datahashes for epoch %d: %w", epoch, err)
	}
	return nil
}

func calculateEpoch(timestamp time.Time) uint64 {
	return uint64(timestamp.Unix() / int64(EpochDuration.Seconds()))
}