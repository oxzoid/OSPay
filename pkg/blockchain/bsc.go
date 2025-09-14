package blockchain

import (
	"context"
	"errors"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	BSC_RPC_URL     = "https://bsc-dataseed.binance.org/"
	BSC_USD_ADDRESS = "0x55d398326f99059fF775485246999027B3197955" // BSC-USD (BUSD-T)
)

var (
	clientOnce sync.Once
	bscClient  *ethclient.Client
	clientErr  error
	// limit concurrent RPC verifications to avoid overloading public RPC
	verifySem = make(chan struct{}, 20)
)

func getClient() (*ethclient.Client, error) {
	clientOnce.Do(func() {
		bscClient, clientErr = ethclient.Dial(BSC_RPC_URL)
	})
	return bscClient, clientErr
}

// VerifyBSCUSDTransfer checks if the given txHash is a BSC-USD transfer to destAddress with the expected amount (in wei)
func VerifyBSCUSDTransfer(txHash string, destAddress string, expectedAmount *big.Int) (bool, error) {
	// throttle concurrent calls
	verifySem <- struct{}{}
	defer func() { <-verifySem }()

	log.Printf("BSC verification: txHash=%s destAddress=%s expectedAmount=%s", txHash, destAddress, expectedAmount.String())

	client, err := getClient()
	if err != nil {
		return false, err
	}

	hash := common.HexToHash(txHash)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	receipt, err := client.TransactionReceipt(ctx, hash)
	if err != nil {
		log.Printf("BSC verification: failed to get receipt for %s: %v", txHash, err)
		return false, err
	}

	log.Printf("BSC verification: got receipt with %d logs", len(receipt.Logs))

	bscUsdAddr := common.HexToAddress(BSC_USD_ADDRESS)
	destAddr := common.HexToAddress(destAddress)

	log.Printf("BSC verification: looking for transfers from BSC-USD contract %s to dest %s", bscUsdAddr.Hex(), destAddr.Hex())

	transferSig := []byte("Transfer(address,address,uint256)")
	transferSigHash := crypto.Keccak256Hash(transferSig)

	for i, vLog := range receipt.Logs {
		log.Printf("BSC verification: log[%d] address=%s topics=%d", i, vLog.Address.Hex(), len(vLog.Topics))

		if vLog.Address == bscUsdAddr && len(vLog.Topics) == 3 && vLog.Topics[0] == transferSigHash {
			to := common.HexToAddress(vLog.Topics[2].Hex())
			amount := new(big.Int).SetBytes(vLog.Data)

			log.Printf("BSC verification: found BSC-USD transfer to=%s amount=%s (expected=%s)", to.Hex(), amount.String(), expectedAmount.String())

			if strings.EqualFold(to.Hex(), destAddr.Hex()) {
				log.Printf("BSC verification: address matches, comparing amounts: %s vs %s", amount.String(), expectedAmount.String())
				if amount.Cmp(expectedAmount) == 0 {
					log.Printf("BSC verification: SUCCESS - amounts match exactly")
					return true, nil
				} else {
					log.Printf("BSC verification: FAIL - amount mismatch")
				}
			} else {
				log.Printf("BSC verification: address mismatch: %s vs %s", to.Hex(), destAddr.Hex())
			}
		}
	}
	log.Printf("BSC verification: no matching BSC-USD transfer found")
	return false, errors.New("no matching BSC-USD transfer found")
}
