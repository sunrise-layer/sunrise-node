package getters

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/celestiaorg/rsmt2d"

	"github.com/sunriselayer/sunrise-da/header"
	"github.com/sunriselayer/sunrise-da/libs/utils"
	"github.com/sunriselayer/sunrise-da/share"
	"github.com/sunriselayer/sunrise-da/share/eds"
	"github.com/sunriselayer/sunrise-da/share/ipld"
)

var _ share.Getter = (*StoreGetter)(nil)

// StoreGetter is a share.Getter that retrieves shares from an eds.Store. No results are saved to
// the eds.Store after retrieval.
type StoreGetter struct {
	store *eds.Store
}

// NewStoreGetter creates a new share.Getter that retrieves shares from an eds.Store.
func NewStoreGetter(store *eds.Store) *StoreGetter {
	return &StoreGetter{
		store: store,
	}
}

// GetShare gets a single share at the given EDS coordinates from the eds.Store through the
// corresponding CAR-level blockstore.
func (sg *StoreGetter) GetShare(ctx context.Context, header *header.ExtendedHeader, row, col int) (share.Share, error) {
	dah := header.DAH
	var err error
	ctx, span := tracer.Start(ctx, "store/get-share", trace.WithAttributes(
		attribute.Int("row", row),
		attribute.Int("col", col),
	))
	defer func() {
		utils.SetStatusAndEnd(span, err)
	}()

	upperBound := len(dah.RowRoots)
	if row >= upperBound || col >= upperBound {
		err := share.ErrOutOfBounds
		span.RecordError(err)
		return nil, err
	}
	root, leaf := ipld.Translate(dah, row, col)
	bs, err := sg.store.CARBlockstore(ctx, dah.Hash())
	if errors.Is(err, eds.ErrNotFound) {
		// convert error to satisfy getter interface contract
		err = share.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getter/store: failed to retrieve blockstore: %w", err)
	}
	defer func() {
		if err := bs.Close(); err != nil {
			log.Warnw("closing blockstore", "err", err)
		}
	}()

	// wrap the read-only CAR blockstore in a getter
	blockGetter := eds.NewBlockGetter(bs)
	s, err := ipld.GetShare(ctx, blockGetter, root, leaf, len(dah.RowRoots))
	if errors.Is(err, ipld.ErrNodeNotFound) {
		// convert error to satisfy getter interface contract
		err = share.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getter/store: failed to retrieve share: %w", err)
	}

	return s, nil
}

// GetEDS gets the EDS identified by the given root from the EDS store.
func (sg *StoreGetter) GetEDS(
	ctx context.Context, header *header.ExtendedHeader,
) (data *rsmt2d.ExtendedDataSquare, err error) {
	ctx, span := tracer.Start(ctx, "store/get-eds")
	defer func() {
		utils.SetStatusAndEnd(span, err)
	}()

	data, err = sg.store.Get(ctx, header.DAH.Hash())
	if errors.Is(err, eds.ErrNotFound) {
		// convert error to satisfy getter interface contract
		err = share.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getter/store: failed to retrieve eds: %w", err)
	}
	return data, nil
}

// GetSharesByNamespace gets all EDS shares in the given namespace from the EDS store through the
// corresponding CAR-level blockstore.
func (sg *StoreGetter) GetSharesByNamespace(
	ctx context.Context,
	header *header.ExtendedHeader,
	namespace share.Namespace,
) (shares share.NamespacedShares, err error) {
	ctx, span := tracer.Start(ctx, "store/get-shares-by-namespace", trace.WithAttributes(
		attribute.String("namespace", namespace.String()),
	))
	defer func() {
		utils.SetStatusAndEnd(span, err)
	}()

	ns, err := eds.RetrieveNamespaceFromStore(ctx, sg.store, header.DAH, namespace)
	if err != nil {
		return nil, fmt.Errorf("getter/store: %w", err)
	}
	return ns, nil
}
