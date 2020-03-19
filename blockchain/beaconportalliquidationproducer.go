package blockchain

import (
	"encoding/base64"
	"encoding/json"
	"github.com/incognitochain/incognito-chain/common"
	"github.com/incognitochain/incognito-chain/database/lvdb"
	"github.com/incognitochain/incognito-chain/metadata"
	"github.com/incognitochain/incognito-chain/wallet"
	"math"
	"strconv"
)

// beacon build instruction for portal liquidation when custodians run away - don't send public tokens back to users.
func buildCustodianRunAwayLiquidationInst(
	redeemID string,
	tokenID string,
	redeemPubTokenAmount uint64,
	mintedCollateralAmount uint64,
	redeemerIncAddrStr string,
	custodianIncAddrStr string,
	metaType int,
	shardID byte,
	status string,
) []string {
	liqCustodianContent := metadata.PortalLiquidateCustodianContent{
		UniqueRedeemID:         redeemID,
		TokenID:                tokenID,
		RedeemPubTokenAmount:   redeemPubTokenAmount,
		MintedCollateralAmount: mintedCollateralAmount,
		RedeemerIncAddressStr:  redeemerIncAddrStr,
		CustodianIncAddressStr: custodianIncAddrStr,
		ShardID:                shardID,
	}
	liqCustodianContentBytes, _ := json.Marshal(liqCustodianContent)
	return []string{
		strconv.Itoa(metaType),
		strconv.Itoa(int(shardID)),
		status,
		string(liqCustodianContentBytes),
	}
}

func buildTopPercentileExchangeRatesLiquidationInst(
	custodianAddress string,
	metaType int,
	status string,
) []string {
	tpContent := metadata.PortalLiquidateTopPercentileExchangeRatesContent{
		CustodianAddress: custodianAddress,
		MetaType: metaType,
		Status: status,
	}
	tpContentBytes, _ := json.Marshal(tpContent)
	return []string{
		strconv.Itoa(metaType),
		"-1",
		status,
		string(tpContentBytes),
	}
}

func buildRedeemLiquidateExchangeRatesInst(
	tokenID string,
	redeemAmount uint64,
	incAddressStr string,
	remoteAddress string,
	redeemFee uint64,
	totalPTokenReceived uint64,
	metaType int,
	shardID byte,
	txReqID common.Hash,
	status string,
) []string {
	redeemRequestContent := metadata.PortalRedeemLiquidateExchangeRatesContent{
		TokenID:                 tokenID,
		RedeemAmount:            redeemAmount,
		RedeemerIncAddressStr:   incAddressStr,
		RemoteAddress:           remoteAddress,
		RedeemFee:               redeemFee,
		TxReqID:                 txReqID,
		ShardID:                 shardID,
		TotalPTokenReceived:     totalPTokenReceived,
	}
	redeemRequestContentBytes, _ := json.Marshal(redeemRequestContent)
	return []string{
		strconv.Itoa(metaType),
		strconv.Itoa(int(shardID)),
		status,
		string(redeemRequestContentBytes),
	}
}

func buildLiquidationCustodianDepositInst(
	pTokenId string,
	incogAddress string,
	depositedAmount uint64,
	freeCollateralSelected bool,
	status string,
	metaType int,
	shardID byte,
	txReqID common.Hash,
) []string {
	redeemRequestContent := metadata.PortalLiquidationCustodianDepositContent{
		PTokenId:			pTokenId,
		IncogAddressStr: 	incogAddress,
		DepositedAmount: 	depositedAmount,
		FreeCollateralSelected: 	freeCollateralSelected,
		TxReqID:                 txReqID,
		ShardID:                 shardID,

	}
	redeemRequestContentBytes, _ := json.Marshal(redeemRequestContent)
	return []string{
		strconv.Itoa(metaType),
		strconv.Itoa(int(shardID)),
		status,
		string(redeemRequestContentBytes),
	}
}

func checkAndBuildInstForCustodianLiquidation(
	beaconHeight uint64,
	currentPortalState *CurrentPortalState,
) ([][]string, error) {

	insts := [][]string{}

	// get exchange rate
	exchangeRateKey := lvdb.NewFinalExchangeRatesKey(beaconHeight)
	exchangeRate := currentPortalState.FinalExchangeRates[exchangeRateKey]
	if exchangeRate == nil {
		Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] Error when get exchange rate")
	}

	for redeemReqKey, redeemReq := range currentPortalState.WaitingRedeemRequests {
		if (beaconHeight + 1) - redeemReq.BeaconHeight >= common.PortalTimeOutCustodianSendPubTokenBack {
			Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] redeemReq.BeaconHeight: %v\n", redeemReq.BeaconHeight)
			Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] beaconHeight: %v\n", beaconHeight)
			// get shardId of redeemer
			redeemerKey, err := wallet.Base58CheckDeserialize(redeemReq.RedeemerAddress)
			if err != nil {
				Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] Error when deserializing redeemer address string in redeemID %v - %v\n: ",
					redeemReq.UniqueRedeemID, err)
				continue
			}
			shardID := common.GetShardIDFromLastByte(redeemerKey.KeySet.PaymentAddress.Pk[len(redeemerKey.KeySet.PaymentAddress.Pk)-1])

			// get tokenID from redeemTokenID
			tokenID := redeemReq.TokenID

			for _, matchCusDetail := range redeemReq.Custodians {
				// calculate minted collateral amount
				mintedAmountInPToken := float64(matchCusDetail.Amount) * float64(common.PercentReceivedCollateralAmount) / 100
				mintedAmountInPRV, err := exchangeRate.ExchangePToken2PRVByTokenId(tokenID, uint64(math.Floor(mintedAmountInPToken)))
				if err != nil {
					Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] Error when exchanging ptoken to prv amount %v\n: ", err)
					inst := buildCustodianRunAwayLiquidationInst(
						redeemReq.UniqueRedeemID,
						redeemReq.TokenID,
						matchCusDetail.Amount,
						0,
						redeemReq.RedeemerAddress,
						matchCusDetail.IncAddress,
						metadata.PortalLiquidateCustodianMeta,
						shardID,
						common.PortalLiquidateCustodianFailedChainStatus,
					)
					insts = append(insts, inst)
					continue
				}

				// update custodian state (total collateral, holding public tokens, locked amount, free collateral)
				Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] cusIncAddr: %v\n", matchCusDetail.IncAddress)
				cusStateKey := lvdb.NewCustodianStateKey(beaconHeight, matchCusDetail.IncAddress)
				custodianState := currentPortalState.CustodianPoolState[cusStateKey]
				if custodianState == nil {
					Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] Error when get custodian state with key %v\n: ", cusStateKey)
					inst := buildCustodianRunAwayLiquidationInst(
						redeemReq.UniqueRedeemID,
						redeemReq.TokenID,
						matchCusDetail.Amount,
						0,
						redeemReq.RedeemerAddress,
						matchCusDetail.IncAddress,
						metadata.PortalLiquidateCustodianMeta,
						shardID,
						common.PortalLiquidateCustodianFailedChainStatus,
					)
					insts = append(insts, inst)
					continue
				}

				if custodianState.TotalCollateral < mintedAmountInPRV ||
					custodianState.LockedAmountCollateral[tokenID] < mintedAmountInPRV {
					Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] Total collateral %v, locked amount %v " +
						"should be greater than minted amount %v\n: ",
						custodianState.TotalCollateral, custodianState.LockedAmountCollateral[tokenID], mintedAmountInPRV)
					inst := buildCustodianRunAwayLiquidationInst(
						redeemReq.UniqueRedeemID,
						redeemReq.TokenID,
						matchCusDetail.Amount,
						mintedAmountInPRV,
						redeemReq.RedeemerAddress,
						matchCusDetail.IncAddress,
						metadata.PortalLiquidateCustodianMeta,
						shardID,
						common.PortalLiquidateCustodianFailedChainStatus,
					)
					insts = append(insts, inst)
					continue
				}

				err = updateCustodianStateAfterLiquidateCustodian(custodianState, mintedAmountInPRV, tokenID)
				if err != nil {
					Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] Error when updating %v\n: ", err)
					inst := buildCustodianRunAwayLiquidationInst(
						redeemReq.UniqueRedeemID,
						redeemReq.TokenID,
						matchCusDetail.Amount,
						mintedAmountInPRV,
						redeemReq.RedeemerAddress,
						matchCusDetail.IncAddress,
						metadata.PortalLiquidateCustodianMeta,
						shardID,
						common.PortalLiquidateCustodianFailedChainStatus,
					)
					insts = append(insts, inst)
					continue
				}

				// remove matching custodian from matching custodians list in waiting redeem request
				currentPortalState.WaitingRedeemRequests[redeemReqKey].Custodians, _ = removeCustodianFromMatchingRedeemCustodians(
					currentPortalState.WaitingRedeemRequests[redeemReqKey].Custodians, matchCusDetail.IncAddress)

				// build instruction
				inst := buildCustodianRunAwayLiquidationInst(
					redeemReq.UniqueRedeemID,
					redeemReq.TokenID,
					matchCusDetail.Amount,
					mintedAmountInPRV,
					redeemReq.RedeemerAddress,
					matchCusDetail.IncAddress,
					metadata.PortalLiquidateCustodianMeta,
					shardID,
					common.PortalLiquidateCustodianSuccessChainStatus,
				)
				insts = append(insts, inst)
			}

			// remove redeem request from waiting redeem requests list
			if len(currentPortalState.WaitingRedeemRequests[redeemReqKey].Custodians) == 0 {
				delete(currentPortalState.WaitingRedeemRequests, redeemReqKey)
			}
		}
	}

	return insts, nil
}

// beacon build instruction for expired waiting porting request - user doesn't send public token to custodian after requesting
func buildExpiredWaitingPortingReqInst(
	portingID string,
	//tokenID string,
	//redeemPubTokenAmount uint64,
	//mintedCollateralAmount uint64,
	//redeemerIncAddrStr string,
	//custodianIncAddrStr string,
	metaType int,
	shardID byte,
	status string,
) []string {
	liqCustodianContent := metadata.PortalExpiredWaitingPortingReqContent{
		UniquePortingID: portingID,
		//TokenID:                tokenID,
		//RedeemPubTokenAmount:   redeemPubTokenAmount,
		//MintedCollateralAmount: mintedCollateralAmount,
		//RedeemerIncAddressStr:  redeemerIncAddrStr,
		//CustodianIncAddressStr: custodianIncAddrStr,
		ShardID:                shardID,
	}
	liqCustodianContentBytes, _ := json.Marshal(liqCustodianContent)
	return []string{
		strconv.Itoa(metaType),
		strconv.Itoa(int(shardID)),
		status,
		string(liqCustodianContentBytes),
	}
}

func checkAndBuildInstForExpiredWaitingPortingRequest(
	beaconHeight uint64,
	currentPortalState *CurrentPortalState,
) ([][]string, error) {

	insts := [][]string{}

	for portingReqKey, portingReq := range currentPortalState.WaitingPortingRequests {
		if (beaconHeight + 1) - portingReq.BeaconHeight >= common.PortalTimeOutCustodianSendPubTokenBack {
			//get shardId of redeemer
			redeemerKey, err := wallet.Base58CheckDeserialize(portingReq.PorterAddress)
			if err != nil {
				Logger.log.Errorf("[checkAndBuildInstForCustodianLiquidation] Error when deserializing redeemer address string in redeemID %v - %v\n: ",
					portingReq.UniquePortingID, err)
				continue
			}
			shardID := common.GetShardIDFromLastByte(redeemerKey.KeySet.PaymentAddress.Pk[len(redeemerKey.KeySet.PaymentAddress.Pk)-1])

			// get tokenID from redeemTokenID
			tokenID := portingReq.TokenID

			// update custodian state in matching custodians list (holding public tokens, locked amount)
			for _, matchCusDetail := range portingReq.Custodians {
				cusStateKey := lvdb.NewCustodianStateKey(beaconHeight, matchCusDetail.IncAddress)
				custodianState := currentPortalState.CustodianPoolState[cusStateKey]
				if custodianState == nil {
					Logger.log.Errorf("[checkAndBuildInstForExpiredWaitingPortingRequest] Error when get custodian state with key %v\n: ", cusStateKey)
					continue
				}

				Logger.log.Errorf("Custodian state before : %v", custodianState.FreeCollateral)

				_ = updateCustodianStateAfterExpiredPortingReq(
					custodianState, matchCusDetail.LockedAmountCollateral, matchCusDetail.Amount, tokenID)

				Logger.log.Errorf("Custodian state after : %v", custodianState.FreeCollateral)
			}

			// remove waiting porting request from waiting list
			delete(currentPortalState.WaitingPortingRequests, portingReqKey)

			// build instruction
			inst := buildExpiredWaitingPortingReqInst(
				portingReq.UniquePortingID,
				metadata.PortalExpiredWaitingPortingReqMeta,
				shardID,
				common.PortalExpiredWaitingPortingReqSuccessChainStatus,
			)
			insts = append(insts, inst)
		}
	}

	return insts, nil
}


func checkAndBuildInstForTPExchangeRateRedeemRequest(
	beaconHeight uint64,
	currentPortalState *CurrentPortalState,
	exchangeRate *lvdb.FinalExchangeRates,
	liquidatedCustodianState *lvdb.CustodianState,
	tokenID string,
)([][]string, error) {
	insts := [][]string{}

	// calculate total amount of matching redeem amount with the liquidated custodian
	totalMatchingRedeemAmountPubToken := uint64(0)
	for _, redeemReq := range currentPortalState.WaitingRedeemRequests {
		if redeemReq.TokenID == tokenID {
			for _, cus := range redeemReq.Custodians {
				if cus.IncAddress == liquidatedCustodianState.IncognitoAddress {
					totalMatchingRedeemAmountPubToken += cus.Amount
				}
			}
		}
	}

	// calculate total minted amount prv for liquidate (maximum 120% amount)
	totalMatchingRedeemAmountPubTokenInPRV, err := exchangeRate.ExchangePToken2PRVByTokenId(tokenID, totalMatchingRedeemAmountPubToken)
	if err != nil {
		Logger.log.Errorf("[checkAndBuildInstForTPExchangeRateRedeemRequest] Error when convert total amount public token to prv %v", err)
		return insts, err
	}

	totalMintedAmountPRV := uint64(math.Floor(float64(totalMatchingRedeemAmountPubTokenInPRV) * float64(common.PercentReceivedCollateralAmount) / float64(100)))
	if totalMintedAmountPRV > liquidatedCustodianState.LockedAmountCollateral[tokenID] {
		totalMintedAmountPRV = liquidatedCustodianState.LockedAmountCollateral[tokenID]
	}

	// calculate minted amount prv for each matching redeem requests
	// rely on percent matching redeem amount and total matching redeem amount
	for redeemReqKey, redeemReq := range currentPortalState.WaitingRedeemRequests {
		if redeemReq.TokenID == tokenID {
			for _, matchCustodian := range redeemReq.Custodians {
				if matchCustodian.IncAddress == liquidatedCustodianState.IncognitoAddress {
					mintedAmountPRV := uint64(math.Floor(float64(matchCustodian.Amount) / float64(totalMatchingRedeemAmountPubToken) * float64(totalMintedAmountPRV)))

					// get shardId of redeemer
					redeemerKey, err := wallet.Base58CheckDeserialize(redeemReq.RedeemerAddress)
					if err != nil {
						Logger.log.Errorf("[checkAndBuildInstForTPExchangeRateRedeemRequest] Error when deserializing redeemer address string in redeemID %v - %v\n: ",
							redeemReq.UniqueRedeemID, err)
						continue
					}
					shardID := common.GetShardIDFromLastByte(redeemerKey.KeySet.PaymentAddress.Pk[len(redeemerKey.KeySet.PaymentAddress.Pk)-1])

					// remove matching custodian from matching custodians list in waiting redeem request
					currentPortalState.WaitingRedeemRequests[redeemReqKey].Custodians, _ = removeCustodianFromMatchingRedeemCustodians(
						currentPortalState.WaitingRedeemRequests[redeemReqKey].Custodians, matchCustodian.IncAddress)

					// build instruction
					inst := buildCustodianRunAwayLiquidationInst(
						redeemReq.UniqueRedeemID,
						redeemReq.TokenID,
						matchCustodian.Amount,
						mintedAmountPRV,
						redeemReq.RedeemerAddress,
						matchCustodian.IncAddress,
						metadata.PortalLiquidateCustodianMeta,
						shardID,
						common.PortalLiquidateCustodianSuccessChainStatus,
					)
					insts = append(insts, inst)

				}
			}
			// remove redeem request from waiting redeem requests list
			if len(currentPortalState.WaitingRedeemRequests[redeemReqKey].Custodians) == 0 {
				delete(currentPortalState.WaitingRedeemRequests, redeemReqKey)
			}
		}
	}
	// update custodian state (update locked amount, holding public token amount)
	custodianStateKey := lvdb.NewCustodianStateKey(beaconHeight, liquidatedCustodianState.IncognitoAddress)
	currentPortalState.CustodianPoolState[custodianStateKey].HoldingPubTokens[tokenID] -= totalMatchingRedeemAmountPubToken
	currentPortalState.CustodianPoolState[custodianStateKey].LockedAmountCollateral[tokenID] -= totalMintedAmountPRV

	return insts, nil
}


/*
Top percentile (TP): 150 (TP150), 130 (TP130), 120 (TP120)
if TP down, we are need liquidation custodian and notify to custodians (or users)
 */
func checkTopPercentileExchangeRatesLiquidationInst(beaconHeight uint64, currentPortalState *CurrentPortalState)  ([][]string, error) {
	if len(currentPortalState.CustodianPoolState) <= 0 {
		return [][]string{}, nil
	}

	insts := [][]string{}

	keyExchangeRate := lvdb.NewFinalExchangeRatesKey(beaconHeight)
	exchangeRate, ok := currentPortalState.FinalExchangeRates[keyExchangeRate]
	if !ok {
		Logger.log.Errorf("Exchange rate not found")
		return [][]string{}, nil
	}

	custodianPoolState := currentPortalState.CustodianPoolState

	for custodianKey, custodianState := range custodianPoolState {
		Logger.log.Infof("Start detect tp custodian key %v", custodianKey)

		Logger.log.Infof("custodian key %v, total pubtokens %v, total amount collateral %v", custodianKey, custodianState.HoldingPubTokens, custodianState.LockedAmountCollateral)

		detectTPExchangeRates, err := detectTPRatio(custodianState.HoldingPubTokens, custodianState.LockedAmountCollateral, exchangeRate)
		if err != nil {
			Logger.log.Errorf("Auto liquidation: Detect tp ratio error %v", err)
			inst := buildTopPercentileExchangeRatesLiquidationInst(
				custodianState.IncognitoAddress,
				metadata.PortalLiquidateTPExchangeRatesMeta,
				common.PortalLiquidateTPExchangeRatesFailedChainStatus,
			)
			insts = append(insts, inst)
			continue
		}

		if len(detectTPExchangeRates) <= 0 {
			Logger.log.Errorf("Detect tp ratio not found")
			inst := buildTopPercentileExchangeRatesLiquidationInst(
				custodianState.IncognitoAddress,
				metadata.PortalLiquidateTPExchangeRatesMeta,
				common.PortalLiquidateTPExchangeRatesFailedChainStatus,
			)
			insts = append(insts, inst)
			continue
		}

		//filter TP by TP 120 & TP130
		resultFilterTp, err := filterTopPercentileLiquidation(custodianState, detectTPExchangeRates)

		if err != nil {
			Logger.log.Errorf("Get liquidate exchange rates change error %v", err)
			inst := buildTopPercentileExchangeRatesLiquidationInst(
				custodianState.IncognitoAddress,
				metadata.PortalLiquidateTPExchangeRatesMeta,
				common.PortalLiquidateTPExchangeRatesFailedChainStatus,
			)
			insts = append(insts, inst)
			continue
		}


		Logger.log.Infof("liquidate exchange rates: filter TP result  %v", resultFilterTp)
		if len(resultFilterTp) > 0 {
			// check and build instruction for waiting redeem request
			for pTokenID, v := range resultFilterTp {
				if v.HoldAmountFreeCollateral > 0 {
					instsFromRedeemRequest, err := checkAndBuildInstForTPExchangeRateRedeemRequest(
						beaconHeight,
						currentPortalState,
						exchangeRate,
						custodianState,
						pTokenID,
					)

					if err != nil {
						Logger.log.Errorf("Error when check and build instruction from redeem request %v\n", err)
						continue
					}


					if len(instsFromRedeemRequest) > 0 {
						Logger.log.Infof("There is %v instructions for tp exchange rate for redeem request", len(instsFromRedeemRequest))
						insts = append(insts, instsFromRedeemRequest...)
					}
				}
			}

			inst := buildTopPercentileExchangeRatesLiquidationInst(
				custodianState.IncognitoAddress,
				metadata.PortalLiquidateTPExchangeRatesMeta,
				common.PortalLiquidateTPExchangeRatesSuccessChainStatus,
			)

			//update custodian
			for pTokenId, v := range resultFilterTp {
				custodianState.LockedAmountCollateral[pTokenId] = custodianState.LockedAmountCollateral[pTokenId] - v.HoldAmountFreeCollateral
				custodianState.HoldingPubTokens[pTokenId] = custodianState.HoldingPubTokens[pTokenId] - v.HoldAmountPubToken
				custodianState.TotalCollateral = custodianState.TotalCollateral - v.HoldAmountFreeCollateral
			}

			currentPortalState.CustodianPoolState[custodianKey] = custodianState

			//update LiquidateExchangeRates
			liquidateExchangeRatesKey := lvdb.NewPortalLiquidateExchangeRatesKey(beaconHeight)
			liquidateExchangeRates, ok := currentPortalState.LiquidateExchangeRates[liquidateExchangeRatesKey]

			Logger.log.Infof("update liquidateExchangeRatesKey key %v", liquidateExchangeRatesKey)
			if !ok {
				item := make(map[string]lvdb.LiquidateExchangeRatesDetail)

				for ptoken, liquidateTopPercentileExchangeRatesDetail := range resultFilterTp {
					item[ptoken] = lvdb.LiquidateExchangeRatesDetail{
						HoldAmountFreeCollateral: liquidateTopPercentileExchangeRatesDetail.HoldAmountFreeCollateral,
						HoldAmountPubToken: liquidateTopPercentileExchangeRatesDetail.HoldAmountPubToken,
					}
				}
				currentPortalState.LiquidateExchangeRates[liquidateExchangeRatesKey], _ = NewLiquidateExchangeRates(item)
			} else {
				for ptoken, liquidateTopPercentileExchangeRatesDetail := range resultFilterTp {
					if _, ok := liquidateExchangeRates.Rates[ptoken]; !ok {
						liquidateExchangeRates.Rates[ptoken] = lvdb.LiquidateExchangeRatesDetail{
							HoldAmountFreeCollateral: liquidateTopPercentileExchangeRatesDetail.HoldAmountFreeCollateral,
							HoldAmountPubToken: liquidateTopPercentileExchangeRatesDetail.HoldAmountPubToken,
						}
					} else {
						liquidateExchangeRates.Rates[ptoken] = lvdb.LiquidateExchangeRatesDetail{
							HoldAmountFreeCollateral: liquidateExchangeRates.Rates[ptoken].HoldAmountFreeCollateral +  liquidateTopPercentileExchangeRatesDetail.HoldAmountFreeCollateral,
							HoldAmountPubToken: liquidateExchangeRates.Rates[ptoken].HoldAmountPubToken +  liquidateTopPercentileExchangeRatesDetail.HoldAmountPubToken,
						}
					}
				}

				currentPortalState.LiquidateExchangeRates[liquidateExchangeRatesKey] = liquidateExchangeRates
			}


			insts = append(insts, inst)
		}
	}

	return insts, nil
}

func (blockchain *BlockChain) buildInstructionsForRedeemLiquidateExchangeRates(
	contentStr string,
	shardID byte,
	metaType int,
	currentPortalState *CurrentPortalState,
	beaconHeight uint64,
) ([][]string, error) {
	// parse instruction
	actionContentBytes, err := base64.StdEncoding.DecodeString(contentStr)
	if err != nil {
		Logger.log.Errorf("ERROR: an error occurred while decoding content string of portal redeem liquidate exchange rate action: %+v", err)
		return [][]string{}, nil
	}
	var actionData metadata.PortalRedeemLiquidateExchangeRatesAction
	err = json.Unmarshal(actionContentBytes, &actionData)
	if err != nil {
		Logger.log.Errorf("ERROR: an error occurred while unmarshal portal redeem liquidate exchange rate action: %+v", err)
		return [][]string{}, nil
	}

	meta := actionData.Meta
	if currentPortalState == nil {
		Logger.log.Warn("Current Portal state is null.")
		// need to mint ptoken to user
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	//get exchange rates
	exchangeRatesKey := lvdb.NewFinalExchangeRatesKey(beaconHeight)
	exchangeRatesState, ok := currentPortalState.FinalExchangeRates[exchangeRatesKey]
	if !ok {
		Logger.log.Errorf("exchange rates not found")
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	minRedeemFee, err := calMinRedeemFee(meta.RedeemAmount, meta.TokenID, exchangeRatesState)
	if err != nil {
		Logger.log.Errorf("Error when calculating minimum redeem fee %v", err)
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	if meta.RedeemFee < minRedeemFee {
		Logger.log.Errorf("Redeem fee is invalid, minRedeemFee %v, but get %v\n", minRedeemFee, meta.RedeemFee)
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	//check redeem amount
	liquidateExchangeRatesKey := lvdb.NewPortalLiquidateExchangeRatesKey(beaconHeight)
	liquidateExchangeRates, ok := currentPortalState.LiquidateExchangeRates[liquidateExchangeRatesKey]

	if !ok {
		Logger.log.Errorf("Liquidate exchange rates not found")
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	liquidateByTokenID, ok := liquidateExchangeRates.Rates[meta.TokenID]

	if !ok {
		Logger.log.Errorf("Liquidate exchange rates not found")
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	totalPrv, err := calTotalLiquidationByExchangeRates(meta.RedeemAmount, liquidateByTokenID)

	if err != nil {
		Logger.log.Errorf("Calculate total liquidation error %v", err)
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	//todo: review
	if totalPrv > liquidateByTokenID.HoldAmountFreeCollateral {
		Logger.log.Errorf("total liquidation error %v", err)
		inst := buildRedeemLiquidateExchangeRatesInst(
			meta.TokenID,
			meta.RedeemAmount,
			meta.RedeemerIncAddressStr,
			meta.RemoteAddress,
			meta.RedeemFee,
			0,
			meta.Type,
			actionData.ShardID,
			actionData.TxReqID,
			common.PortalRedeemLiquidateExchangeRatesRejectedChainStatus,
		)
		return [][]string{inst}, nil
	}

	liquidateExchangeRates.Rates[meta.TokenID] = lvdb.LiquidateExchangeRatesDetail{
		HoldAmountFreeCollateral: liquidateByTokenID.HoldAmountFreeCollateral - totalPrv,
		HoldAmountPubToken: liquidateByTokenID.HoldAmountPubToken - meta.RedeemAmount,
	}

	currentPortalState.LiquidateExchangeRates[liquidateExchangeRatesKey] = liquidateExchangeRates

	inst := buildRedeemLiquidateExchangeRatesInst(
		meta.TokenID,
		meta.RedeemAmount,
		meta.RedeemerIncAddressStr,
		meta.RemoteAddress,
		meta.RedeemFee,
		0,
		meta.Type,
		actionData.ShardID,
		actionData.TxReqID,
		common.PortalRedeemLiquidateExchangeRatesSuccessChainStatus,
	)
	return [][]string{inst}, nil
}

func (blockchain *BlockChain) buildInstructionsForLiquidationCustodianDeposit(
	contentStr string,
	shardID byte,
	metaType int,
	currentPortalState *CurrentPortalState,
	beaconHeight uint64,
) ([][]string, error) {
	// parse instruction
	actionContentBytes, err := base64.StdEncoding.DecodeString(contentStr)
	if err != nil {
		Logger.log.Errorf("ERROR: an error occurred while decoding content string of portal liquidation custodian deposit action: %+v", err)
		return [][]string{}, nil
	}
	var actionData metadata.PortalLiquidationCustodianDepositAction
	err = json.Unmarshal(actionContentBytes, &actionData)
	if err != nil {
		Logger.log.Errorf("ERROR: an error occurred while unmarshal portal liquidation custodian deposit action: %+v", err)
		return [][]string{}, nil
	}

	if currentPortalState == nil {
		Logger.log.Warn("Current Portal state is null.")
		// need to refund collateral to custodian
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	meta := actionData.Meta

	keyCustodianState := lvdb.NewCustodianStateKey(beaconHeight, meta.IncogAddressStr)

	custodian, ok := currentPortalState.CustodianPoolState[keyCustodianState]

	if !ok {
		Logger.log.Errorf("Custodian not found")
		// need to refund collateral to custodian
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	//check exit ptoken
	if _, ok := custodian.LockedAmountCollateral[actionData.Meta.PTokenId]; !ok {
		Logger.log.Errorf("PToken not found")
		// need to refund collateral to custodian
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	keyExchangeRate := lvdb.NewFinalExchangeRatesKey(beaconHeight)
	exchangeRate, ok := currentPortalState.FinalExchangeRates[keyExchangeRate]
	if !ok {
		Logger.log.Errorf("Exchange rate not found", err)
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}


	detectTPExchangeRates, err := detectTPRatio(custodian.HoldingPubTokens, custodian.LockedAmountCollateral, exchangeRate)
	if err != nil {
		Logger.log.Errorf("Custodian deposit: Detect tp ratio error %v", err)
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	tpValue, ok := detectTPExchangeRates[actionData.Meta.PTokenId]

	if !ok {
		Logger.log.Errorf("Ptoken not found from detect TP Exchange rates")
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	isTp20, ok := IsTP120(tpValue)
	if !ok {
		Logger.log.Errorf("TP value is %v, value is must TP130 | TP120", tpValue)
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	if isTp20 {
		Logger.log.Errorf("TP value is %v, value is must TP130", tpValue)
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	//check deposit amount
	totalPToken := up150Percent(custodian.HoldingPubTokens[actionData.Meta.PTokenId])
	totalPRV, err := exchangeRate.ExchangePToken2PRVByTokenId(actionData.Meta.PTokenId, totalPToken)

	if err != nil {
		Logger.log.Errorf("Convert exchange rates error %v", err)
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}


    totalPRVNeeded := totalPRV - custodian.LockedAmountCollateral[actionData.Meta.PTokenId]

	if actionData.Meta.DepositedAmount < totalPRVNeeded {
		Logger.log.Errorf("Deposited amount is not enough, expect %v, data sent %v", totalPRVNeeded, actionData.Meta.DepositedAmount)
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	//todo: review paid all free collateral or a part free collateral
	if actionData.Meta.FreeCollateralSelected == true &&  custodian.FreeCollateral < totalPRVNeeded {
		Logger.log.Errorf("Deposited amount is not enough, expect %v, freeCollateral is %v", totalPRVNeeded, custodian.FreeCollateral)
		inst := buildLiquidationCustodianDepositInst(
			actionData.Meta.PTokenId,
			actionData.Meta.IncogAddressStr,
			actionData.Meta.DepositedAmount,
			actionData.Meta.FreeCollateralSelected,
			common.PortalLiquidationCustodianDepositRejectedChainStatus,
			actionData.Meta.Type,
			shardID,
			actionData.TxReqID,
		)

		return [][]string{inst}, nil
	}

	Logger.log.Errorf("Deposited amount: expect %v, data sent %v", totalPRVNeeded, actionData.Meta.DepositedAmount)
	//deposit from DepositAmount
	//minimum prv deposit
	if actionData.Meta.FreeCollateralSelected == false {
		custodian.TotalCollateral = custodian.TotalCollateral + actionData.Meta.DepositedAmount
		custodian.LockedAmountCollateral[actionData.Meta.PTokenId] = custodian.LockedAmountCollateral[actionData.Meta.PTokenId] + actionData.Meta.DepositedAmount
	} else {
		//deposit from free collateral DepositedAmount
		custodian.LockedAmountCollateral[actionData.Meta.PTokenId] = custodian.LockedAmountCollateral[actionData.Meta.PTokenId] + actionData.Meta.DepositedAmount
		custodian.FreeCollateral = custodian.FreeCollateral - actionData.Meta.DepositedAmount
	}

	currentPortalState.CustodianPoolState[keyCustodianState] = custodian

	inst := buildLiquidationCustodianDepositInst(
		actionData.Meta.PTokenId,
		actionData.Meta.IncogAddressStr,
		actionData.Meta.DepositedAmount,
		actionData.Meta.FreeCollateralSelected,
		common.PortalLiquidationCustodianDepositSuccessChainStatus,
		actionData.Meta.Type,
		shardID,
		actionData.TxReqID,
	)

	return [][]string{inst}, nil
}