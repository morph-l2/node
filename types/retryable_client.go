package types

import (
	"context"
	"github.com/cenkalti/backoff/v4"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/log"
	"math/big"
	"strings"
)

const ConnectionRefused = "connection refused"

type RetryableClient struct {
	authClient *authclient.Client
	ethClient  *ethclient.Client
	b          backoff.BackOff
}

// NewRetryableClient make the client retryable
// Will retry calling the api, if the connection is refused
func NewRetryableClient(authClient *authclient.Client, ethClient *ethclient.Client) *RetryableClient {
	return &RetryableClient{
		authClient: authClient,
		ethClient:  ethClient,
		b:          backoff.NewExponentialBackOff(),
	}
}

func (rc *RetryableClient) AssembleL2Block(ctx context.Context, number *big.Int, transactions eth.Transactions) (ret *catalyst.ExecutableL2Data, err error) {
	if retryErr := backoff.Retry(func() error {
		resp, respErr := rc.authClient.AssembleL2Block(ctx, number, transactions)
		if respErr != nil {
			log.Warn("failed to AssembleL2Block", "error", err)
			if strings.Contains(respErr.Error(), ConnectionRefused) {
				return respErr
			}
			err = respErr // stop retrying and put this error to response error field, if the error is not connection related
		}
		ret = resp
		return nil
	}, rc.b); retryErr != nil {
		return nil, retryErr
	}
	return
}

func (rc *RetryableClient) ValidateL2Block(ctx context.Context, executableL2Data *catalyst.ExecutableL2Data) (ret bool, err error) {
	if retryErr := backoff.Retry(func() error {
		resp, respErr := rc.authClient.ValidateL2Block(ctx, executableL2Data)
		if respErr != nil {
			log.Warn("failed to ValidateL2Block", "error", err)
			if strings.Contains(respErr.Error(), ConnectionRefused) {
				return respErr
			}
			err = respErr
		}
		ret = resp
		return nil
	}, rc.b); retryErr != nil {
		return false, retryErr
	}
	return
}

func (rc *RetryableClient) NewL2Block(ctx context.Context, executableL2Data *catalyst.ExecutableL2Data, blsData *eth.BLSData) (err error) {
	if retryErr := backoff.Retry(func() error {
		respErr := rc.authClient.NewL2Block(ctx, executableL2Data, blsData)
		if respErr != nil {
			log.Warn("failed to NewL2Block", "error", err)
			if strings.Contains(respErr.Error(), ConnectionRefused) {
				return respErr
			}
			err = respErr
		}
		return nil
	}, rc.b); retryErr != nil {
		return retryErr
	}
	return
}

func (rc *RetryableClient) NewSafeL2Block(ctx context.Context, safeL2Data *catalyst.SafeL2Data, blsData *eth.BLSData) (ret *eth.Header, err error) {
	if retryErr := backoff.Retry(func() error {
		resp, respErr := rc.authClient.NewSafeL2Block(ctx, safeL2Data, blsData)
		if respErr != nil {
			log.Warn("failed to NewSafeL2Block", "error", err)
			if strings.Contains(respErr.Error(), ConnectionRefused) {
				return respErr
			}
			err = respErr
		}
		ret = resp
		return nil
	}, rc.b); retryErr != nil {
		return nil, retryErr
	}
	return
}

func (rc *RetryableClient) BlockNumber(ctx context.Context) (ret uint64, err error) {
	if retryErr := backoff.Retry(func() error {
		resp, respErr := rc.ethClient.BlockNumber(ctx)
		if respErr != nil {
			log.Warn("failed to call BlockNumber", "error", err)
			if strings.Contains(respErr.Error(), ConnectionRefused) {
				return respErr
			}
			err = respErr
		}
		ret = resp
		return nil
	}, rc.b); retryErr != nil {
		return 0, retryErr
	}
	return
}
