package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distributiontypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v6/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v6/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v6/modules/core/exported"
	"github.com/gogo/protobuf/proto"
	"github.com/persistenceOne/persistence-sdk/v2/utils"
	epochstypes "github.com/persistenceOne/persistence-sdk/v2/x/epochs/types"
	ibchookertypes "github.com/persistenceOne/persistence-sdk/v2/x/ibchooker/types"

	lscosmostypes "github.com/persistenceOne/pstake-native/v2/x/lscosmos/types"
)

// BeforeEpochStart - call hook if registered
func (k Keeper) BeforeEpochStart(ctx sdk.Context, epochIdentifier string, epochNumber int64) error {
	return nil
}

// AfterEpochEnd handle the "stake", "reward" and "undelegate" epoch and their respective actions
// 1. "stake" generates delegate transaction for delegating the amount of stake accumulated over the "stake" epoch
// 2. "reward" generates delegate transaction for withdrawing and restaking the amount of stake accumulated over the "reward" epochs
// and shift the amount to next epoch if the min amount is not reached
// 3. "undelegate" generated the undelegate transaction for undelegating the amount accumulated over the "undelegate" epoch
func (k Keeper) AfterEpochEnd(ctx sdk.Context, epochIdentifier string, epochNumber int64) error {
	//params := k.GetParams(ctx)
	if !k.GetModuleState(ctx) {
		return nil
	}
	hostChainParams := k.GetHostChainParams(ctx)
	k.Logger(ctx).Info(fmt.Sprintf("Starting AfterEndEpoch for epochIdentifier %s, epochNumber %v", epochIdentifier, epochNumber))
	if epochIdentifier == lscosmostypes.DelegationEpochIdentifier {
		wrapperFn := func(ctx sdk.Context) error {
			return k.DelegationEpochWorkFlow(ctx, hostChainParams)
		}
		err := utils.ApplyFuncIfNoError(ctx, wrapperFn)
		if err != nil {
			k.Logger(ctx).Error("Failed DelegationEpochIdentifier Function with:", "err: ", err)
		}
	}
	if epochIdentifier == lscosmostypes.RewardEpochIdentifier {
		wrapperFn := func(ctx sdk.Context) error {
			return k.RewardEpochEpochWorkFlow(ctx, hostChainParams)
		}
		err := utils.ApplyFuncIfNoError(ctx, wrapperFn)
		if err != nil {
			k.Logger(ctx).Error("Failed RewardEpochIdentifier Function with:", "err: ", err)
		}
	}
	if epochIdentifier == lscosmostypes.UndelegationEpochIdentifier && epochNumber%lscosmostypes.UndelegationEpochNumberFactor == 0 {
		wrapperFn := func(ctx sdk.Context) error {
			return k.UndelegationEpochWorkFlow(ctx, hostChainParams, epochNumber)
		}
		err := utils.ApplyFuncIfNoError(ctx, wrapperFn)
		if err != nil {
			k.Logger(ctx).Error("Failed UndelegationEpochIdentifier Function with:", "err: ", err)
			// Fail the unbonding for current epoch
			currentUnbondingEpochNumber := lscosmostypes.CurrentUnbondingEpoch(epochNumber)
			hostAccountUndelegationForEpoch, err := k.GetHostAccountUndelegationForEpoch(ctx, epochNumber)
			if err != nil {
				return err
			}
			err = k.RemoveHostAccountUndelegation(ctx, currentUnbondingEpochNumber)
			if err != nil {
				return err
			}
			k.FailUnbondingEpochCValue(ctx, currentUnbondingEpochNumber, hostAccountUndelegationForEpoch.TotalUndelegationAmount)
			k.Logger(ctx).Info(fmt.Sprintf("Failed unbonding for undelegationEpoch: %v", currentUnbondingEpochNumber))

		}
	}
	return nil
}

// ___________________________________________________________________________________________________

// EpochsHooks wrapper struct for incentives keeper
type EpochsHooks struct {
	k Keeper
}

var _ epochstypes.EpochHooks = EpochsHooks{}

// NewEpochHooks Return the wrapper struct
func (k Keeper) NewEpochHooks() EpochsHooks {
	return EpochsHooks{k}
}

// BeforeEpochStart new epoch is next block of epoch end block
func (h EpochsHooks) BeforeEpochStart(ctx sdk.Context, epochIdentifier string, epochNumber int64) error {
	return h.k.BeforeEpochStart(ctx, epochIdentifier, epochNumber)
}

// AfterEpochEnd gets called at the end of the epoch, end of epoch is the timestamp of first block
// produced after epoch duration.
func (h EpochsHooks) AfterEpochEnd(ctx sdk.Context, epochIdentifier string, epochNumber int64) error {
	return h.k.AfterEpochEnd(ctx, epochIdentifier, epochNumber)
}

// DelegationEpochWorkFlow handles the delegation epoch work flow :
// 1. Checks and adds balance present in the rewards module account to delegation module account
// 2. Checks and adds balance present in the deposit module account to delegation module account
// 3. Checks and IBC transfers the balance present in the delegation module account to other chain
// 4. Transfer fees to the fee address in the host chain params
func (k Keeper) DelegationEpochWorkFlow(ctx sdk.Context, hostChainParams lscosmostypes.HostChainParams) error {
	// greater than min amount, transfer from deposit to delegation, to ibctransfer.
	// Right now we only do baseDenom
	ibcDenom := k.GetIBCDenom(ctx)

	allDepositBalances := k.bankKeeper.GetAllBalances(ctx, authtypes.NewModuleAddress(lscosmostypes.DepositModuleAccount))
	depositBalance := sdk.NewCoin(ibcDenom, allDepositBalances.AmountOf(ibcDenom))
	if depositBalance.Amount.GT(sdk.ZeroInt()) {
		err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, lscosmostypes.DepositModuleAccount, lscosmostypes.DelegationModuleAccount, sdk.NewCoins(depositBalance))
		if err != nil {
			k.Logger(ctx).Error("Could not send amount from ", lscosmostypes.DepositModuleAccount, " module account to ",
				lscosmostypes.DelegationModuleAccount)
			return err
		}
	}
	allDelegationBalances := k.bankKeeper.GetAllBalances(ctx, authtypes.NewModuleAddress(lscosmostypes.DelegationModuleAccount))
	delegationBalance := sdk.NewCoin(ibcDenom, allDelegationBalances.AmountOf(ibcDenom))
	if delegationBalance.IsPositive() && depositBalance.IsPositive() && delegationBalance.IsGTE(depositBalance) {
		delegationState := k.GetDelegationState(ctx)
		_, clientState, err := k.channelKeeper.GetChannelClientState(ctx, hostChainParams.TransferPort, hostChainParams.TransferChannel)
		if err != nil {
			k.Logger(ctx).Error(fmt.Sprintf("Error getting client state %s", err))
			return err
		}
		timeoutHeight := clienttypes.NewHeight(clientState.GetLatestHeight().GetRevisionNumber(), clientState.GetLatestHeight().GetRevisionHeight()+lscosmostypes.IBCTimeoutHeightIncrement)

		msg := ibctransfertypes.NewMsgTransfer(hostChainParams.TransferPort, hostChainParams.TransferChannel,
			depositBalance, authtypes.NewModuleAddress(lscosmostypes.DelegationModuleAccount).String(),
			delegationState.HostChainDelegationAddress, timeoutHeight, 0, "")

		handler := k.msgRouter.Handler(msg)

		res, err := handler(ctx, msg)
		if err != nil {
			k.Logger(ctx).Error(fmt.Sprintf("could not send transfer msg via MsgServiceRouter, error: %s", err))
			return err
		}
		ctx.EventManager().EmitEvents(res.GetEvents())

		k.AddIBCTransferToTransientStore(ctx, depositBalance)
	}
	// move extra tokens to pstake address - anyone can send tokens to delegation address.
	// deposit address is deny-listed address - can only accept tokens via transactions, so should not have any extra tokens
	// should be transferred to pstake address.
	remainingDelegationBalance := k.bankKeeper.GetAllBalances(ctx, authtypes.NewModuleAddress(lscosmostypes.DelegationModuleAccount))

	if remainingDelegationBalance.IsAllPositive() {
		feeAddr, err := sdk.AccAddressFromBech32(hostChainParams.PstakeParams.PstakeFeeAddress)
		if err != nil {
			return err
		}
		err = k.bankKeeper.SendCoinsFromModuleToAccount(ctx, lscosmostypes.DelegationModuleAccount, feeAddr, remainingDelegationBalance)
		if err != nil {
			k.Logger(ctx).Error(fmt.Sprintf("could not send remaining balance: %s in delegationModuleAccount: %s with error: %s", remainingDelegationBalance, lscosmostypes.DelegationModuleAccount, err))
			return err
		}
	}

	return nil
}

// RewardEpochEpochWorkFlow handles the rewards epoch work flow generates and executes the
// ICA transaction for claiming rewards
func (k Keeper) RewardEpochEpochWorkFlow(ctx sdk.Context, hostChainParams lscosmostypes.HostChainParams) error {
	// send withdraw rewards from delegators.
	delegationState := k.GetDelegationState(ctx)
	hostAccounts := k.GetHostAccounts(ctx)
	if len(delegationState.HostAccountDelegations) == 0 {
		//return early
		return nil
	}
	withdrawRewardMsgs := make([]proto.Message, len(delegationState.HostAccountDelegations))
	for i, delegation := range delegationState.HostAccountDelegations {
		withdrawRewardMsgs[i] = &distributiontypes.MsgWithdrawDelegatorReward{
			DelegatorAddress: delegationState.HostChainDelegationAddress,
			ValidatorAddress: delegation.ValidatorAddress,
		}
	}
	err := k.GenerateAndExecuteICATx(ctx, hostChainParams.ConnectionID, hostAccounts.DelegatorAccountOwnerID, withdrawRewardMsgs)
	return err
	// on Ack do icq for reward acc. balance of uatom
	// callback for sending it to delegation account
	// on Ack delegate txn
}

// UndelegationEpochWorkFlow handles the undelegation epoch work flow :
// 1. Fetches host account undelegations using in GetHostAccountUndelegationForEpoch
// 2. Convert stk coin to token using ConvertStkToToken based on the current c value
// 3. Form undelegation messages using the current delegation state
// 4. Generate and execute the ICA transaction for the undelegation messages
// 5. Perform KV store changes based on the previous actions
// Returns nil or an error based on the checks in the function
func (k Keeper) UndelegationEpochWorkFlow(ctx sdk.Context, hostChainParams lscosmostypes.HostChainParams, epochNumber int64) error {
	// currentEpoch always equals epochNumber during undelegation.
	currentEpoch := lscosmostypes.CurrentUnbondingEpoch(epochNumber)
	hostAccountUndelegationForEpoch, err := k.GetHostAccountUndelegationForEpoch(ctx, currentEpoch)
	if err != nil {
		k.Logger(ctx).Info(fmt.Sprintf("No undelegations for epochNumber: %v", epochNumber))
		return nil
	}

	cValue := k.GetCValue(ctx)
	amountToUnstake, _ := k.ConvertStkToToken(ctx, sdk.NewDecCoinFromCoin(hostAccountUndelegationForEpoch.TotalUndelegationAmount), cValue)
	amountToUnstake = sdk.NewCoin(hostChainParams.BaseDenom, amountToUnstake.Amount)
	if amountToUnstake.IsNil() || !amountToUnstake.IsPositive() {
		k.Logger(ctx).Info("atoms to undelegate too low")
		return nil
	}
	allowListedValidators := k.GetAllowListedValidators(ctx)
	if len(allowListedValidators.AllowListedValidators) == 0 {
		return lscosmostypes.ErrInValidAllowListedValidators
	}
	delegationState := k.GetDelegationState(ctx)
	undelegateMsgs, undelegationEntries, err := k.UndelegateMsgs(ctx, amountToUnstake.Amount, hostChainParams.BaseDenom, delegationState)
	if err != nil {
		return err
	}
	hostAccounts := k.GetHostAccounts(ctx)
	err = k.GenerateAndExecuteICATx(ctx, hostChainParams.ConnectionID, hostAccounts.DelegatorAccountOwnerID, undelegateMsgs)
	if err != nil {
		return err
	}
	// add undelegation entries to db (update completion time onAck)
	k.AddEntriesForUndelegationEpoch(ctx, currentEpoch, undelegationEntries)
	//optimistic about this -> it retries till the ICA passes, if ICA undelegate fails the module is paused.
	k.SetUnbondingEpochCValue(ctx, lscosmostypes.UnbondingEpochCValue{
		EpochNumber:    currentEpoch,
		STKBurn:        hostAccountUndelegationForEpoch.TotalUndelegationAmount,
		AmountUnbonded: amountToUnstake,
		IsMatured:      false,
		IsFailed:       false,
	})

	return nil
}

// ___________________________________________________________________________________________________

// OnRecvIBCTransferPacket performs the following steps :
// 1. Checks if the acknowledgment was a success or not
// 2. Clears transient entries based on packet contents
// 3. Update the matured unbonding epoch c value using MatureUnbondingEpochCValue
func (k Keeper) OnRecvIBCTransferPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress, transferAck ibcexported.Acknowledgement) error {
	if !transferAck.Success() {
		// Do nothing
		return nil
	}
	var transferPacketData ibctransfertypes.FungibleTokenPacketData
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(packet.GetData(), &transferPacketData); err != nil {
		return err
	}
	hostChainParams := k.GetHostChainParams(ctx)
	delegationState := k.GetDelegationState(ctx)
	//Checks
	channel, found := k.channelKeeper.GetChannel(ctx, hostChainParams.TransferPort, hostChainParams.TransferChannel)
	if !found {
		return channeltypes.ErrChannelNotFound
	}
	if packet.GetSourceChannel() != channel.Counterparty.ChannelId ||
		packet.GetSourcePort() != channel.Counterparty.PortId {
		// no need to return err, since most likely code is expected to enter this condition
		return nil
	}

	if transferPacketData.GetSender() != delegationState.HostChainDelegationAddress ||
		transferPacketData.GetReceiver() != authtypes.NewModuleAddress(lscosmostypes.UndelegationModuleAccount).String() ||
		transferPacketData.GetDenom() != hostChainParams.BaseDenom {
		// no need to return err, since most likely code is expected to enter this condition
		return nil
	}
	amount, ok := sdk.NewIntFromString(transferPacketData.GetAmount())
	if !ok {
		return ibctransfertypes.ErrInvalidAmount
	}
	k.Logger(ctx).Info(fmt.Sprintf("atoms tokens successfully transferred to controller chain address %s, amount: %s, denom: %s", transferPacketData.Receiver, transferPacketData.Amount, transferPacketData.Denom))

	removedTransientUndelegationTransfer, err := k.RemoveUndelegationTransferFromTransientStore(ctx, sdk.NewCoin(transferPacketData.GetDenom(), amount))
	if err != nil {
		return err
	}
	k.MatureUnbondingEpochCValue(ctx, removedTransientUndelegationTransfer.EpochNumber)
	return nil
}

// OnAcknowledgementIBCTransferPacket performs the following steps :
// 1. Returns early if there is an error
// 2. Performs health checks the packet received
// 3. Updates balance in the delegation state  by using AddBalanceToDelegationState
// 4. Removes amount from IBCTransferFromTransientStore
func (k Keeper) OnAcknowledgementIBCTransferPacket(ctx sdk.Context, packet channeltypes.Packet, acknowledgement []byte, relayer sdk.AccAddress, transferAckErr error) error {

	if transferAckErr != nil {
		return nil
	}
	var ack channeltypes.Acknowledgement
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(acknowledgement, &ack); err != nil {
		return err
	}
	if !ack.Success() {
		return channeltypes.ErrInvalidAcknowledgement
	}
	var data ibctransfertypes.FungibleTokenPacketData
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(packet.GetData(), &data); err != nil {
		return err
	}
	// check for tokens moved from delegationModuleAccount to it's ica counterpart.
	hostChainParams := k.GetHostChainParams(ctx)
	delegationState := k.GetDelegationState(ctx)
	if packet.GetSourceChannel() != hostChainParams.TransferChannel ||
		packet.GetSourcePort() != hostChainParams.TransferPort {
		// no need to return err, since most likely code is expected to enter this condition
		return nil
	}

	if data.GetSender() != authtypes.NewModuleAddress(lscosmostypes.DelegationModuleAccount).String() ||
		data.GetReceiver() != delegationState.HostChainDelegationAddress ||
		data.GetDenom() != ibctransfertypes.GetPrefixedDenom(hostChainParams.TransferPort, hostChainParams.TransferChannel, hostChainParams.BaseDenom) {
		// no need to return err, since most likely code is expected to enter this condition
		return nil
	}
	k.Logger(ctx).Info(fmt.Sprintf("atoms tokens successfully transferred to host chain address %s, amount: %s, denom: %s", data.Receiver, data.Amount, data.Denom))

	amount, ok := sdk.NewIntFromString(data.GetAmount())
	if !ok {
		return ibctransfertypes.ErrInvalidAmount
	}
	ibcDenom := ibctransfertypes.ParseDenomTrace(data.GetDenom())
	k.AddBalanceToDelegationState(ctx, sdk.NewCoin(hostChainParams.BaseDenom, amount))
	k.RemoveIBCTransferFromTransientStore(ctx, sdk.NewCoin(ibcDenom.IBCDenom(), amount))
	return nil
}

// OnTimeoutIBCTransferPacket performs the following actions :
// 1. Returns early if there is a packet timeout error
// 2. Perform health checks on the packet received
// 3. Remove the amount from IBCTransferFromTransientStore
func (k Keeper) OnTimeoutIBCTransferPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress, transferTimeoutErr error) error {
	// transient store needs to be reverted here.
	if transferTimeoutErr != nil {
		return transferTimeoutErr
	}
	var data ibctransfertypes.FungibleTokenPacketData
	if err := ibctransfertypes.ModuleCdc.UnmarshalJSON(packet.GetData(), &data); err != nil {
		return err
	}
	// check for tokens moved from delegationModuleAccount to it's ica counterpart.
	hostChainParams := k.GetHostChainParams(ctx)
	delegationState := k.GetDelegationState(ctx)
	if packet.GetSourceChannel() != hostChainParams.TransferChannel ||
		packet.GetSourcePort() != hostChainParams.TransferPort {
		// no need to return err, since most likely code is expected to enter this condition
		return nil
	}

	if data.GetSender() != authtypes.NewModuleAddress(lscosmostypes.DelegationModuleAccount).String() ||
		data.GetReceiver() != delegationState.HostChainDelegationAddress ||
		data.GetDenom() != ibctransfertypes.GetPrefixedDenom(hostChainParams.TransferPort, hostChainParams.TransferChannel, hostChainParams.BaseDenom) {
		// no need to return err, since most likely code is expected to enter this condition
		return nil
	}
	k.Logger(ctx).Info(fmt.Sprintf("atoms tokens timedout while transferring to host chain address %s, amount: %s, denom: %s", data.Receiver, data.Amount, data.Denom))

	amount, ok := sdk.NewIntFromString(data.GetAmount())
	if !ok {
		return ibctransfertypes.ErrInvalidAmount
	}
	ibcDenom := ibctransfertypes.ParseDenomTrace(data.GetDenom())
	k.RemoveIBCTransferFromTransientStore(ctx, sdk.NewCoin(ibcDenom.IBCDenom(), amount))
	//move tokens to deposit account
	return k.bankKeeper.SendCoinsFromModuleToModule(ctx, lscosmostypes.DelegationModuleAccount, lscosmostypes.DepositModuleAccount, sdk.NewCoins(sdk.NewCoin(ibcDenom.IBCDenom(), amount)))
}

type IBCTransferHooks struct {
	k Keeper
}

var _ ibchookertypes.IBCHandshakeHooks = IBCTransferHooks{}

func (k Keeper) NewIBCTransferHooks() IBCTransferHooks {
	return IBCTransferHooks{k}
}

func (i IBCTransferHooks) OnRecvPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress, transferAck ibcexported.Acknowledgement) error {
	return i.k.OnRecvIBCTransferPacket(ctx, packet, relayer, transferAck)
}

func (i IBCTransferHooks) OnAcknowledgementPacket(ctx sdk.Context, packet channeltypes.Packet, acknowledgement []byte, relayer sdk.AccAddress, transferAckErr error) error {
	return i.k.OnAcknowledgementIBCTransferPacket(ctx, packet, acknowledgement, relayer, transferAckErr)

}

func (i IBCTransferHooks) OnTimeoutPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress, transferTimeoutErr error) error {
	return i.k.OnTimeoutIBCTransferPacket(ctx, packet, relayer, transferTimeoutErr)
}
