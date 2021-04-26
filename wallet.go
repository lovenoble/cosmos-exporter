package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distributiontypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

func WalletHandler(w http.ResponseWriter, r *http.Request, grpcConn *grpc.ClientConn) {
	sublogger := log.With().
		Str("request-id", uuid.New().String()).
		Logger()

	address := r.URL.Query().Get("address")
	myAddress, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		sublogger.Error().
			Str("address", address).
			Err(err).
			Msg("Could not get address")
		return
	}

	walletBalanceGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cosmos_wallet_balance",
			Help: "Balance of the Cosmos-based blockchain wallet",
		},
		[]string{"address", "denom"},
	)

	walletDelegationGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cosmos_wallet_delegations",
			Help: "Delegations of the Cosmos-based blockchain wallet",
		},
		[]string{"address", "denom", "delegated_to"},
	)

	walletRedelegationGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cosmos_wallet_redelegations",
			Help: "Redlegations of the Cosmos-based blockchain wallet",
		},
		[]string{"address", "denom", "redelegated_from", "redelegated_to"},
	)

	walletUnbondingsGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cosmos_wallet_unbondings",
			Help: "Unbondings of the Cosmos-based blockchain wallet",
		},
		[]string{"address", "denom", "unbonded_from"},
	)

	walletRewardsGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cosmos_wallet_rewards",
			Help: "Rewards of the Cosmos-based blockchain wallet",
		},
		[]string{"address", "denom", "validator_address"},
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(walletBalanceGauge)
	registry.MustRegister(walletDelegationGauge)
	registry.MustRegister(walletUnbondingsGauge)
	registry.MustRegister(walletRedelegationGauge)
	registry.MustRegister(walletRewardsGauge)

	var wg sync.WaitGroup

	go func() {
		defer wg.Done()

		bankClient := banktypes.NewQueryClient(grpcConn)
		bankRes, err := bankClient.Balance(
			context.Background(),
			&banktypes.QueryBalanceRequest{Address: myAddress.String(), Denom: *Denom},
		)

		if err != nil {
			sublogger.Error().
				Str("address", address).
				Err(err).
				Msg("Could not get balance")
			return
		}

		walletBalanceGauge.With(prometheus.Labels{
			"address": address,
			"denom":   bankRes.GetBalance().Denom,
		}).Set(float64(bankRes.GetBalance().Amount.Int64()))
	}()
	wg.Add(1)

	go func() {
		defer wg.Done()

		stakingClient := stakingtypes.NewQueryClient(grpcConn)
		stakingRes, err := stakingClient.DelegatorDelegations(
			context.Background(),
			&stakingtypes.QueryDelegatorDelegationsRequest{DelegatorAddr: myAddress.String()},
		)

		if err != nil {
			sublogger.Error().
				Str("address", address).
				Err(err).
				Msg("Could not get delegations")
			return
		}

		for _, delegation := range stakingRes.DelegationResponses {
			walletDelegationGauge.With(prometheus.Labels{
				"address":      address,
				"denom":        delegation.Balance.Denom,
				"delegated_to": delegation.Delegation.ValidatorAddress,
			}).Set(float64(delegation.Balance.Amount.Int64()))
		}
	}()
	wg.Add(1)

	go func() {
		defer wg.Done()

		stakingClient := stakingtypes.NewQueryClient(grpcConn)
		stakingRes, err := stakingClient.DelegatorUnbondingDelegations(
			context.Background(),
			&stakingtypes.QueryDelegatorUnbondingDelegationsRequest{DelegatorAddr: myAddress.String()},
		)

		if err != nil {
			sublogger.Error().
				Str("address", address).
				Err(err).
				Msg("Could not get unbonding delegations")
			return
		}

		for _, unbonding := range stakingRes.UnbondingResponses {
			var sum float64 = 0
			for _, entry := range unbonding.Entries {
				sum += float64(entry.Balance.Int64())
			}

			walletUnbondingsGauge.With(prometheus.Labels{
				"address":       unbonding.DelegatorAddress,
				"denom":         *Denom, // unbonding does not have denom in response for some reason
				"unbonded_from": unbonding.ValidatorAddress,
			}).Set(sum)
		}
	}()
	wg.Add(1)

	go func() {
		defer wg.Done()

		stakingClient := stakingtypes.NewQueryClient(grpcConn)
		stakingRes, err := stakingClient.Redelegations(
			context.Background(),
			&stakingtypes.QueryRedelegationsRequest{DelegatorAddr: myAddress.String()},
		)

		if err != nil {
			sublogger.Error().
				Str("address", address).
				Err(err).
				Msg("Could not get redelegations")
			return
		}

		for _, redelegation := range stakingRes.RedelegationResponses {
			var sum float64 = 0
			for _, entry := range redelegation.Entries {
				sum += float64(entry.Balance.Int64())
			}

			walletRedelegationGauge.With(prometheus.Labels{
				"address":          redelegation.Redelegation.DelegatorAddress,
				"denom":            *Denom, // redelegation does not have denom in response for some reason
				"redelegated_from": redelegation.Redelegation.ValidatorSrcAddress,
				"redelegated_to":   redelegation.Redelegation.ValidatorDstAddress,
			}).Set(sum)
		}
	}()
	wg.Add(1)

	go func() {
		defer wg.Done()

		distributionClient := distributiontypes.NewQueryClient(grpcConn)
		distributionRes, err := distributionClient.DelegationTotalRewards(
			context.Background(),
			&distributiontypes.QueryDelegationTotalRewardsRequest{DelegatorAddress: myAddress.String()},
		)
		if err != nil {
			sublogger.Error().
				Str("address", address).
				Err(err).
				Msg("Could not get rewards")
			return
		}

		for _, reward := range distributionRes.Rewards {
			for _, entry := range reward.Reward {
				walletRewardsGauge.With(prometheus.Labels{
					"address":           address,
					"denom":             entry.Denom,
					"validator_address": reward.ValidatorAddress,
				}).Set(float64(entry.Amount.RoundInt64()))
			}
		}
	}()
	wg.Add(1)

	wg.Wait()

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
	sublogger.Info().
		Str("method", "GET").
		Str("endpoint", "/metrics/wallet?address="+address).
		Msg("Request processed")
}
