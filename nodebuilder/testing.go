package nodebuilder

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	libhead "github.com/celestiaorg/go-header"
	apptypes "github.com/sunriselayer/sunrise/x/blob/types"

	"github.com/sunriselayer/sunrise-da/core"
	"github.com/sunriselayer/sunrise-da/header"
	"github.com/sunriselayer/sunrise-da/header/headertest"
	"github.com/sunriselayer/sunrise-da/libs/fxutil"
	"github.com/sunriselayer/sunrise-da/nodebuilder/node"
	"github.com/sunriselayer/sunrise-da/nodebuilder/p2p"
	"github.com/sunriselayer/sunrise-da/nodebuilder/state"
)

// MockStore provides mock in memory Store for testing purposes.
func MockStore(t *testing.T, cfg *Config) Store {
	t.Helper()
	store := NewMemStore()
	err := store.PutConfig(cfg)
	require.NoError(t, err)
	return store
}

func TestNode(t *testing.T, tp node.Type, opts ...fx.Option) *Node {
	return TestNodeWithConfig(t, tp, DefaultConfig(tp), opts...)
}

func TestNodeWithConfig(t *testing.T, tp node.Type, cfg *Config, opts ...fx.Option) *Node {
	// avoids port conflicts
	cfg.RPC.Port = "0"
	cfg.Header.TrustedPeers = []string{"/ip4/1.2.3.4/tcp/12345/p2p/12D3KooWNaJ1y1Yio3fFJEXCZyd1Cat3jmrPdgkYCrHfKD3Ce21p"}

	store := MockStore(t, cfg)
	ks, err := store.Keystore()
	require.NoError(t, err)

	opts = append(opts,
		// avoid writing keyring on disk
		state.WithKeyringSigner(TestKeyringSigner(t, ks.Keyring())),
		// temp dir for the eds store FIXME: Should be in mem
		fx.Replace(node.StorePath(t.TempDir())),
		// avoid requesting trustedPeer during initialization
		fxutil.ReplaceAs(headertest.NewStore(t), new(libhead.Store[*header.ExtendedHeader])),
	)

	// in fact, we don't need core.Client in tests, but Bridge requires is a valid one
	// or fails otherwise with failed attempt to connect with custom build client
	if tp == node.Bridge {
		cctx := core.StartTestNode(t)
		opts = append(opts,
			fxutil.ReplaceAs(cctx.Client, new(core.Client)),
		)
	}

	nd, err := New(tp, p2p.Private, store, opts...)
	require.NoError(t, err)
	return nd
}

func TestKeyringSigner(t *testing.T, ring keyring.Keyring) *apptypes.KeyringSigner {
	signer := apptypes.NewKeyringSigner(ring, "", string(p2p.Private))
	_, _, err := signer.NewMnemonic("my_sunrise_key", keyring.English, "",
		"", hd.Secp256k1)
	require.NoError(t, err)
	return signer
}
