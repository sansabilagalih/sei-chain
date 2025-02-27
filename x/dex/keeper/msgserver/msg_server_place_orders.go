package msgserver

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/sei-protocol/sei-chain/x/dex/types"
	typesutils "github.com/sei-protocol/sei-chain/x/dex/types/utils"
	dexutils "github.com/sei-protocol/sei-chain/x/dex/utils"
)

func (k msgServer) transferFunds(goCtx context.Context, msg *types.MsgPlaceOrders) error {
	if len(msg.Funds) == 0 {
		return nil
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	contractAddr := sdk.MustAccAddressFromBech32(msg.ContractAddr)
	if err := k.BankKeeper.IsSendEnabledCoins(ctx, msg.Funds...); err != nil {
		return err
	}
	if k.BankKeeper.BlockedAddr(contractAddr) {
		return sdkerrors.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", contractAddr.String())
	}

	sender := sdk.MustAccAddressFromBech32(msg.Creator)
	for _, fund := range msg.Funds {
		if fund.Amount.IsNil() || fund.IsNegative() {
			return errors.New("fund deposits cannot be nil or negative")
		}
		dexutils.GetMemState(ctx.Context()).GetDepositInfo(ctx, typesutils.ContractAddress(msg.GetContractAddr())).Add(&types.DepositInfoEntry{
			Creator: msg.Creator,
			Denom:   fund.Denom,
			Amount:  sdk.NewDec(fund.Amount.Int64()),
		})
	}
	if err := k.BankKeeper.DeferredSendCoinsFromAccountToModule(ctx, sender, types.ModuleName, msg.Funds); err != nil {
		return fmt.Errorf("error sending coins to contract: %s", err)
	}
	return nil
}

func (k msgServer) PlaceOrders(goCtx context.Context, msg *types.MsgPlaceOrders) (*types.MsgPlaceOrdersResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	defer ctx.ContextMemCache().IncrMetricCounter(uint32(len(msg.Orders)), sdk.ORDER_COUNT)

	if err := msg.ValidateBasic(); err != nil {
		ctx.Logger().Error(fmt.Sprintf("request invalid: %s", err))
		return nil, err
	}

	if err := k.transferFunds(goCtx, msg); err != nil {
		return nil, err
	}
	events := []sdk.Event{}
	nextID := k.GetNextOrderID(ctx, msg.ContractAddr)
	idsInResp := []uint64{}
	for _, order := range msg.GetOrders() {
		priceTicksize, found := k.Keeper.GetPriceTickSizeForPair(ctx, msg.GetContractAddr(), types.Pair{PriceDenom: order.PriceDenom, AssetDenom: order.AssetDenom})
		if !found {
			return nil, sdkerrors.Wrapf(sdkerrors.ErrKeyNotFound, "the pair {price:%s,asset:%s} has no price ticksize configured", order.PriceDenom, order.AssetDenom)
		}
		quantityTicksize, found := k.Keeper.GetQuantityTickSizeForPair(ctx, msg.GetContractAddr(), types.Pair{PriceDenom: order.PriceDenom, AssetDenom: order.AssetDenom})
		if !found {
			return nil, sdkerrors.Wrapf(sdkerrors.ErrKeyNotFound, "the pair {price:%s,asset:%s} has no quantity ticksize configured", order.PriceDenom, order.AssetDenom)
		}
		pair := types.Pair{PriceDenom: order.PriceDenom, AssetDenom: order.AssetDenom, PriceTicksize: &priceTicksize, QuantityTicksize: &quantityTicksize}
		pairStr := typesutils.GetPairString(&pair)
		order.Id = nextID
		order.Account = msg.Creator
		order.ContractAddr = msg.GetContractAddr()
		dexutils.GetMemState(ctx.Context()).GetBlockOrders(ctx, typesutils.ContractAddress(msg.GetContractAddr()), pairStr).Add(order)
		idsInResp = append(idsInResp, nextID)
		events = append(events, sdk.NewEvent(
			types.EventTypePlaceOrder,
			sdk.NewAttribute(types.AttributeKeyOrderID, fmt.Sprint(nextID)),
		))
		nextID++
	}
	k.SetNextOrderID(ctx, msg.ContractAddr, nextID)
	ctx.EventManager().EmitEvents(events)
	return &types.MsgPlaceOrdersResponse{
		OrderIds: idsInResp,
	}, nil
}
