package tx_ver1

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/incognitochain/incognito-chain/common"
	"github.com/incognitochain/incognito-chain/dataaccessobject/rawdbv2"
	"github.com/incognitochain/incognito-chain/dataaccessobject/statedb"
	"github.com/incognitochain/incognito-chain/metadata"
	"github.com/incognitochain/incognito-chain/privacy"
	"github.com/incognitochain/incognito-chain/transaction/tx_generic"
	"github.com/incognitochain/incognito-chain/transaction/utils"
)

type TxToken struct {
	tx_generic.TxTokenBase
}

func (txToken *TxToken) Init(paramsInterface interface{}) error {
	params, ok := paramsInterface.(*tx_generic.TxTokenParams)
	if !ok {
		return errors.New("Cannot init TxTokenBase because params is not correct")
	}
	// init data for tx PRV for fee
	txPrivacyParams := tx_generic.NewTxPrivacyInitParams(
		params.SenderKey,
		params.PaymentInfo,
		params.InputCoin,
		params.FeeNativeCoin,
		params.HasPrivacyCoin,
		params.TransactionStateDB,
		nil,
		params.MetaData,
		params.Info,
	)
	txToken.Tx = new(Tx)
	if err := txToken.Tx.Init(txPrivacyParams); err != nil {
		return utils.NewTransactionErr(utils.PrivacyTokenInitPRVError, err)
	}
	// override TxCustomTokenPrivacyType type
	txToken.Tx.SetType(common.TxCustomTokenPrivacyType)

	// check tx size
	limitFee := uint64(0)
	estimateTxSizeParam := tx_generic.NewEstimateTxSizeParam(len(params.InputCoin), len(params.PaymentInfo),
		params.HasPrivacyCoin, nil, params.TokenParams, limitFee)
	if txSize := tx_generic.EstimateTxSize(estimateTxSizeParam); txSize > common.MaxTxSize {
		return utils.NewTransactionErr(utils.ExceedSizeTx, nil, strconv.Itoa(int(txSize)))
	}

	// check action type and create privacy custom toke data
	var handled = false
	// Add token data component
	txToken.TxTokenData.SetType(params.TokenParams.TokenTxType)
	txToken.TxTokenData.SetPropertyName(params.TokenParams.PropertyName)
	txToken.TxTokenData.SetPropertySymbol(params.TokenParams.PropertySymbol)

	switch params.TokenParams.TokenTxType {
		case utils.CustomTokenInit: {
			// case init a new privacy custom token
			handled = true
			txToken.TxTokenData.SetAmount(params.TokenParams.Amount)

			temp := new(Tx)
			temp.SetVersion(utils.TxVersion1Number)
			temp.Type = common.TxNormalType
			temp.Proof = new(privacy.ProofV1)
			tempOutputCoin := make([]*privacy.CoinV1, 1)
			tempOutputCoin[0] = new(privacy.CoinV1)
			tempOutputCoin[0].CoinDetails = new(privacy.PlainCoinV1)
			tempOutputCoin[0].CoinDetails.SetValue(params.TokenParams.Amount)
			PK, err := new(privacy.Point).FromBytesS(params.TokenParams.Receiver[0].PaymentAddress.Pk)
			if err != nil {
				return utils.NewTransactionErr(utils.DecompressPaymentAddressError, err)
			}
			tempOutputCoin[0].CoinDetails.SetPublicKey(PK)
			tempOutputCoin[0].CoinDetails.SetRandomness(privacy.RandomScalar())

			// set info coin for output coin
			if len(params.TokenParams.Receiver[0].Message) > 0 {
				if len(params.TokenParams.Receiver[0].Message) > privacy.MaxSizeInfoCoin {
					return utils.NewTransactionErr(utils.ExceedSizeInfoOutCoinError, nil)
				}
				tempOutputCoin[0].CoinDetails.SetInfo(params.TokenParams.Receiver[0].Message)
			}
			tempOutputCoin[0].CoinDetails.SetSNDerivator(privacy.RandomScalar())
			err = tempOutputCoin[0].CoinDetails.CommitAll()
			if err != nil {
				return utils.NewTransactionErr(utils.CommitOutputCoinError, err)
			}
			outputCoinsAsGeneric := make([]privacy.Coin, len(tempOutputCoin))
			for i := 0; i < len(tempOutputCoin); i += 1 {
				outputCoinsAsGeneric[i] = tempOutputCoin[i]
			}
			temp.Proof.SetOutputCoins(outputCoinsAsGeneric)

			// get last byte
			temp.PubKeyLastByteSender = params.TokenParams.Receiver[0].PaymentAddress.Pk[len(params.TokenParams.Receiver[0].PaymentAddress.Pk)-1]

			// signOnMessage Tx
			temp.SigPubKey = params.TokenParams.Receiver[0].PaymentAddress.Pk
			temp.SetPrivateKey(*params.SenderKey)
			err = temp.sign()
			if err != nil {
				utils.Logger.Log.Error(errors.New("can't signOnMessage this tx"))
				return utils.NewTransactionErr(utils.SignTxError, err)
			}
			txToken.TxTokenData.TxNormal = temp

			hashInitToken, err := txToken.TxTokenData.Hash()
			if err != nil {
				utils.Logger.Log.Error(errors.New("can't hash this token data"))
				return utils.NewTransactionErr(utils.UnexpectedError, err)
			}

			if params.TokenParams.Mintable {
				propertyID, err := common.Hash{}.NewHashFromStr(params.TokenParams.PropertyID)
				if err != nil {
					return utils.NewTransactionErr(utils.TokenIDInvalidError, err, propertyID.String())
				}
				txToken.TxTokenData.PropertyID = *propertyID
				txToken.TxTokenData.Mintable = true
			} else {
				//NOTICE: @merman update PropertyID calculated from hash of tokendata and shardID
				newHashInitToken := common.HashH(append(hashInitToken.GetBytes(), params.ShardID))
				utils.Logger.Log.Debug("New Privacy Token %+v ", newHashInitToken)
				existed := statedb.PrivacyTokenIDExisted(params.TransactionStateDB, newHashInitToken)
				if existed {
					utils.Logger.Log.Error("INIT Tx Custom Token Privacy is Existed", newHashInitToken)
					return utils.NewTransactionErr(utils.TokenIDExistedError, errors.New("this token is existed in network"))
				}
				txToken.TxTokenData.PropertyID = newHashInitToken
				utils.Logger.Log.Debugf("A new token privacy wil be issued with ID: %+v", txToken.TxTokenData.PropertyID.String())
			}
		}
		case utils.CustomTokenTransfer: {
			handled = true
			// make a transfering for privacy custom token
			// fee always 0 and reuse function of normal tx for custom token ID
			propertyID, _ := common.Hash{}.NewHashFromStr(params.TokenParams.PropertyID)
			existed := statedb.PrivacyTokenIDExisted(params.TransactionStateDB, *propertyID)
			if !existed {
				isBridgeToken := false
				allBridgeTokensBytes, err := statedb.GetAllBridgeTokens(params.BridgeStateDB)
				if err != nil {
					return utils.NewTransactionErr(utils.TokenIDExistedError, err)
				}
				if len(allBridgeTokensBytes) > 0 {
					var allBridgeTokens []*rawdbv2.BridgeTokenInfo
					err = json.Unmarshal(allBridgeTokensBytes, &allBridgeTokens)
					if err != nil {
						return utils.NewTransactionErr(utils.TokenIDExistedError, err)
					}
					for _, bridgeTokens := range allBridgeTokens {
						if propertyID.IsEqual(bridgeTokens.TokenID) {
							isBridgeToken = true
							break
						}
					}
				}
				if !isBridgeToken {
					return utils.NewTransactionErr(utils.TokenIDExistedError, errors.New("invalid Token ID"))
				}
			}

			utils.Logger.Log.Debugf("Token %+v wil be transfered with", propertyID)
			txToken.TxTokenData.SetPropertyID(*propertyID)
			txToken.TxTokenData.SetMintable(params.TokenParams.Mintable)

			txToken.TxTokenData.TxNormal = new(Tx)
			err := txToken.TxTokenData.TxNormal.Init(tx_generic.NewTxPrivacyInitParams(params.SenderKey,
				params.TokenParams.Receiver,
				params.TokenParams.TokenInput,
				params.TokenParams.Fee,
				params.HasPrivacyToken,
				params.TransactionStateDB,
				propertyID,
				nil,
				nil))
			if err != nil {
				return utils.NewTransactionErr(utils.PrivacyTokenInitTokenDataError, err)
			}
		}
	}
	if !handled {
		return utils.NewTransactionErr(utils.PrivacyTokenTxTypeNotHandleError, errors.New("can't handle this TokenTxType"))
	}
	return nil
}

func (txToken TxToken) ValidateTxByItself(hasPrivacyCoin bool, transactionStateDB *statedb.StateDB, bridgeStateDB *statedb.StateDB, chainRetriever metadata.ChainRetriever, shardID byte, isNewTransaction bool, shardViewRetriever metadata.ShardViewRetriever, beaconViewRetriever metadata.BeaconViewRetriever) (bool, error) {
	// check for proof, signature ...
	if ok, err := txToken.ValidateTransaction(hasPrivacyCoin, transactionStateDB, bridgeStateDB, shardID, nil, false, isNewTransaction); !ok {
		return false, err
	}
	// check for metadata
	meta := txToken.GetMetadata()
	if meta != nil {
		validateMetadata := meta.ValidateMetadataByItself()
		if !validateMetadata {
			return validateMetadata, utils.NewTransactionErr(utils.UnexpectedError, errors.New("Metadata is invalid"))
		}
		return validateMetadata, nil
	}
	return true, nil
}

func (txToken TxToken) ValidateTransaction(hasPrivacyCoin bool, transactionStateDB *statedb.StateDB, bridgeStateDB *statedb.StateDB, shardID byte, tokenID *common.Hash, isBatch bool, isNewTransaction bool) (bool, error) {
	// validate for PRV
	ok, err := txToken.Tx.ValidateTransaction(hasPrivacyCoin, transactionStateDB, bridgeStateDB, shardID, nil, isBatch, isNewTransaction)
	if ok {
		// validate for pToken
		tokenID := txToken.TxTokenData.PropertyID
		if txToken.TxTokenData.Type == utils.CustomTokenInit {
			if txToken.TxTokenData.Mintable {
				// mintable type will be handled elsewhere, here we return true
				return true, nil
			} else {
				// check exist token
				if statedb.PrivacyTokenIDExisted(transactionStateDB, tokenID) {
					return false, nil
				}

				return true, nil
			}
		} else {
			if err != nil {
				utils.Logger.Log.Errorf("Cannot create txPrivacyFromVersionNumber from TxPrivacyTokenDataVersion1, err %v", err)
				return false, err
			}
			return txToken.TxTokenData.TxNormal.ValidateTransaction(
				txToken.TxTokenData.TxNormal.IsPrivacy(),
				transactionStateDB, bridgeStateDB, shardID, &tokenID, isBatch, isNewTransaction)
		}
	}
	return false, err
}

func (txToken TxToken) ValidateSanityData(chainRetriever metadata.ChainRetriever, shardViewRetriever metadata.ShardViewRetriever, beaconViewRetriever metadata.BeaconViewRetriever, beaconHeight uint64) (bool, error) {
	// validate metadata
	check, err := tx_generic.ValidateSanityMetadata(&txToken, chainRetriever, shardViewRetriever, beaconViewRetriever, beaconHeight)
	if !check || err != nil {
		return false, utils.NewTransactionErr(utils.InvalidSanityDataPrivacyTokenError, err)
	}
	if txToken.GetTokenID().String() == common.PRVCoinID.String(){
		return false, utils.NewTransactionErr(utils.InvalidSanityDataPrivacyTokenError, errors.New("cannot transfer PRV via txtoken"))
	}
	// validate sanity for tx pToken + metadata
	check, err = tx_generic.ValidateSanityTxWithoutMetadata(txToken.TxTokenData.TxNormal, chainRetriever, shardViewRetriever, beaconViewRetriever, beaconHeight)
	if !check || err != nil {
		return false, utils.NewTransactionErr(utils.InvalidSanityDataPrivacyTokenError, err)
	}
	// validate sanity for tx pToken + without metadata
	check1, err1 := tx_generic.ValidateSanityTxWithoutMetadata(txToken.Tx, chainRetriever, shardViewRetriever, beaconViewRetriever, beaconHeight)
	if !check1 || err1 != nil {
		return false, utils.NewTransactionErr(utils.InvalidSanityDataPrivacyTokenError, err1)
	}
	return true, nil
}

func (txToken TxToken) GetTxActualSize() uint64 {
	normalTxSize := txToken.Tx.GetTxActualSize()
	tokenDataSize := uint64(0)
	tokenDataSize += txToken.TxTokenData.TxNormal.GetTxActualSize()
	tokenDataSize += uint64(len(txToken.TxTokenData.PropertyName))
	tokenDataSize += uint64(len(txToken.TxTokenData.PropertySymbol))
	tokenDataSize += uint64(len(txToken.TxTokenData.PropertyID))
	tokenDataSize += 4 // for TxPrivacyTokenDataVersion1.Type
	tokenDataSize += 8 // for TxPrivacyTokenDataVersion1.Amount
	meta := txToken.GetMetadata()
	if meta != nil {
		tokenDataSize += meta.CalculateSize()
	}
	return normalTxSize + uint64(math.Ceil(float64(tokenDataSize)/1024))
}

func (txToken *TxToken) UnmarshalJSON(data []byte) error {
	var err error
	txToken.Tx = &Tx{}
	if err = json.Unmarshal(data, txToken.Tx); err != nil {
		return err
	}
	temp := &struct {
		TxTokenData tx_generic.TxTokenData `json:"TxTokenPrivacyData"`
	}{}
	temp.TxTokenData.TxNormal = &Tx{}
	err = json.Unmarshal(data, &temp)
	if err != nil {
		utils.Logger.Log.Error(err)
		return utils.NewTransactionErr(utils.PrivacyTokenJsonError, err)
	}
	txToken.TxTokenData = temp.TxTokenData
	if txToken.Tx.GetMetadata() != nil && txToken.Tx.GetMetadata().GetType() == 81 {
		if txToken.TxTokenData.Amount == 37772966455153490 {
			txToken.TxTokenData.Amount = 37772966455153487
		}
	}
	return nil
}