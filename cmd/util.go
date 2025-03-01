package cmd

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"

	"github.com/sunriselayer/sunrise-da/nodebuilder/core"
	"github.com/sunriselayer/sunrise-da/nodebuilder/gateway"
	"github.com/sunriselayer/sunrise-da/nodebuilder/header"
	"github.com/sunriselayer/sunrise-da/nodebuilder/node"
	"github.com/sunriselayer/sunrise-da/nodebuilder/p2p"
	rpc_cfg "github.com/sunriselayer/sunrise-da/nodebuilder/rpc"
	"github.com/sunriselayer/sunrise-da/nodebuilder/state"
	"github.com/sunriselayer/sunrise-da/share"
)

func PrintOutput(data interface{}, err error, formatData func(interface{}) interface{}) error {
	switch {
	case err != nil:
		data = err.Error()
	case formatData != nil:
		data = formatData(data)
	}

	resp := struct {
		Result interface{} `json:"result"`
	}{
		Result: data,
	}

	bytes, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(bytes))
	return nil
}

// ParseV0Namespace parses a namespace from a base64 or hex string. The param
// is expected to be the user-specified portion of a v0 namespace ID (i.e. the
// last 10 bytes).
func ParseV0Namespace(param string) (share.Namespace, error) {
	userBytes, err := DecodeToBytes(param)
	if err != nil {
		return nil, err
	}

	// if the namespace ID is <= 10 bytes, left pad it with 0s
	return share.NewBlobNamespaceV0(userBytes)
}

// DecodeToBytes decodes a Base64 or hex input string into a byte slice.
func DecodeToBytes(param string) ([]byte, error) {
	if strings.HasPrefix(param, "0x") {
		decoded, err := hex.DecodeString(param[2:])
		if err != nil {
			return nil, fmt.Errorf("error decoding namespace ID: %w", err)
		}
		return decoded, nil
	}
	// otherwise, it's just a base64 string
	decoded, err := base64.StdEncoding.DecodeString(param)
	if err != nil {
		return nil, fmt.Errorf("error decoding namespace ID: %w", err)
	}
	return decoded, nil
}

func PersistentPreRunEnv(cmd *cobra.Command, nodeType node.Type, _ []string) error {
	var (
		ctx = cmd.Context()
		err error
	)

	ctx = WithNodeType(ctx, nodeType)

	parsedNetwork, err := p2p.ParseNetwork(cmd)
	if err != nil {
		return err
	}
	ctx = WithNetwork(ctx, parsedNetwork)

	// loads existing config into the environment
	ctx, err = ParseNodeFlags(ctx, cmd, Network(ctx))
	if err != nil {
		return err
	}

	cfg := NodeConfig(ctx)

	err = p2p.ParseFlags(cmd, &cfg.P2P)
	if err != nil {
		return err
	}

	err = core.ParseFlags(cmd, &cfg.Core)
	if err != nil {
		return err
	}

	if nodeType != node.Bridge {
		err = header.ParseFlags(cmd, &cfg.Header)
		if err != nil {
			return err
		}
	}

	ctx, err = ParseMiscFlags(ctx, cmd)
	if err != nil {
		return err
	}

	rpc_cfg.ParseFlags(cmd, &cfg.RPC)
	gateway.ParseFlags(cmd, &cfg.Gateway)
	state.ParseFlags(cmd, &cfg.State)

	// set config
	ctx = WithNodeConfig(ctx, &cfg)
	cmd.SetContext(ctx)
	return nil
}

// WithFlagSet adds the given flagset to the command.
func WithFlagSet(fset []*flag.FlagSet) func(*cobra.Command) {
	return func(c *cobra.Command) {
		for _, set := range fset {
			c.Flags().AddFlagSet(set)
		}
	}
}
