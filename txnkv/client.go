// Copyright 2021 TiKV Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package txnkv

import (
	"context"
	"fmt"

	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/pkg/errors"
	"github.com/tikv/client-go/v2/config"
	"github.com/tikv/client-go/v2/internal/retry"
	"github.com/tikv/client-go/v2/oracle"
	"github.com/tikv/client-go/v2/tikv"
	"github.com/tikv/client-go/v2/txnkv/transaction"
	"github.com/tikv/client-go/v2/util"
	pd "github.com/tikv/pd/client"
)

// Client is a txn client.
type Client struct {
	*tikv.KVStore
}

type option struct {
	apiVersion   kvrpcpb.APIVersion
	keyspaceName string
	spKVPrefix   string
}

// ClientOpt is factory to set the client options.
type ClientOpt func(*option)

// WithKeyspace is used to set client's keyspace.
func WithKeyspace(keyspaceName string) ClientOpt {
	return func(opt *option) {
		opt.keyspaceName = keyspaceName
	}
}

// WithAPIVersion is used to set client's apiVersion.
func WithAPIVersion(apiVersion kvrpcpb.APIVersion) ClientOpt {
	return func(opt *option) {
		opt.apiVersion = apiVersion
	}
}

// WithSafePointKVPrefix is used to set client's safe point kv prefix.
func WithSafePointKVPrefix(prefix string) ClientOpt {
	return func(opt *option) {
		opt.spKVPrefix = prefix
	}
}

// NewClient creates a txn client with pdAddrs.
func NewClient(pdAddrs []string, opts ...ClientOpt) (*Client, error) {
	// Apply options.
	opt := &option{}
	for _, o := range opts {
		o(opt)
	}

	var (
		pdClient   pd.Client
		apiContext pd.APIContext
		err        error
	)
	switch opt.apiVersion {
	case kvrpcpb.APIVersion_V1:
		apiContext = pd.NewAPIContextV1()
	case kvrpcpb.APIVersion_V2:
		apiContext = pd.NewAPIContextV2(opt.keyspaceName)
	default:
		return nil, errors.Errorf("unknown API version: %d", opt.apiVersion)
	}
	pdClient, err = tikv.NewPDClientWithAPIContext(pdAddrs, apiContext)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	pdClient = util.InterceptedPDClient{Client: pdClient}

	// Construct codec from options.
	var codecCli *tikv.CodecPDClient
	switch opt.apiVersion {
	case kvrpcpb.APIVersion_V1:
		codecCli = tikv.NewCodecPDClient(tikv.ModeTxn, pdClient)
	case kvrpcpb.APIVersion_V2:
		keyspaceMeta, err := pdClient.LoadKeyspace(context.Background(), opt.keyspaceName)
		if err != nil {
			return nil, err
		}
		codecCli, err = tikv.NewCodecPDClientWithKeyspaceMeta(tikv.ModeTxn, pdClient, keyspaceMeta)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.Errorf("unknown API version: %d", opt.apiVersion)
	}

	pdClient = codecCli

	cfg := config.GetGlobalConfig()
	// init uuid
	uuid := fmt.Sprintf("tikv-%v", pdClient.GetClusterID(context.TODO()))
	tlsConfig, err := cfg.Security.ToTLSConfig()
	if err != nil {
		return nil, err
	}

	spkv, err := tikv.NewEtcdSafePointKV(pdAddrs, tlsConfig, tikv.WithPrefix(opt.spKVPrefix))
	if err != nil {
		return nil, err
	}

	rpcClient := tikv.NewRPCClient(tikv.WithSecurity(cfg.Security), tikv.WithCodec(codecCli.GetCodec()))

	s, err := tikv.NewKVStore(uuid, pdClient, spkv, rpcClient)
	if err != nil {
		return nil, err
	}
	if cfg.TxnLocalLatches.Enabled {
		s.EnableTxnLocalLatches(cfg.TxnLocalLatches.Capacity)
	}
	return &Client{KVStore: s}, nil
}

// GetTimestamp returns the current global timestamp.
func (c *Client) GetTimestamp(ctx context.Context) (uint64, error) {
	bo := retry.NewBackofferWithVars(ctx, transaction.TsoMaxBackoff, nil)
	startTS, err := c.GetTimestampWithRetry(bo, oracle.GlobalTxnScope)
	if err != nil {
		return 0, err
	}
	return startTS, nil
}
