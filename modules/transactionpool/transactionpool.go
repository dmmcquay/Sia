package transactionpool

import (
	"errors"
	"sync"

	"github.com/NebulousLabs/Sia/consensus"
	"github.com/NebulousLabs/Sia/hash"
)

var (
	ConflictingTransactionErr = errors.New("conflicting transaction exists within transaction pool")
)

type unconfirmedTransaction struct {
	transaction  consensus.Transaction
	requirements []*unconfirmedTransaction
	dependents   []*unconfirmedTransaction
}

type TransactionPool struct {
	state *consensus.State

	usedOutputs map[consensus.OutputID]*unconfirmedTransaction
	newOutputs  map[consensus.OutputID]*unconfirmedTransaction

	storageProofs map[consensus.BlockHeight]map[hash.Hash]consensus.Transaction

	transactionList map[consensus.OutputID]*unconfirmedTransaction

	mu sync.RWMutex
}

func (tp *TransactionPool) storeProofTransaction(t consensus.Transaction) (err error) {
	// Sanity Check - transaction should contain at least 1 storage proof.
	if consensus.DEBUG {
		if len(t.StorageProofs) < 1 {
			panic("misuse of storeProofTransaction")
		}
	}

	// Check that each storage proof acts on an existing contract in the
	// blockchain.
	var greatestHeight consensus.BlockHeight
	for _, sp := range t.StorageProofs {
		var contract consensus.FileContract
		_, err = tp.state.Contract(sp.ContractID)
		if err != nil {
			return
		}

		// Track the highest start height of the contracts that the proofs
		// fulfill.
		if contract.Start > greatestHeight {
			greatestHeight = contract.Start
		}
	}

	// Put the transaction in the proof map.
	heightMap, exists := tp.storageProofs[greatestHeight]
	if !exists {
		tp.storageProofs[greatestHeight] = make(map[hash.Hash]consensus.Transaction)
		heightMap = tp.storageProofs[greatestHeight]
	}
	_, exists = heightMap[hash.HashObject(t)]
	if exists {
		err = errors.New("transaction already known")
		return
	}
	heightMap[hash.HashObject(t)] = t
	return
}

func (tp *TransactionPool) conflict(t consensus.Transaction) bool {
	// Check that the inputs are not in conflict with other transactions.
	for _, input := range t.Inputs {
		_, exists := tp.usedOutputs[input.OutputID]
		if exists {
			return true
		}
	}
	return false
}

func (tp *TransactionPool) AcceptTransaction(t consensus.Transaction) (err error) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Check that the transaction follows 'Standard.md' guidelines.
	err = standard(t)
	if err != nil {
		return
	}

	// Handle the transaction differently if it contains a storage proof.
	if len(t.StorageProofs) != 0 {
		err = tp.storeProofTransaction(t)
		if err != nil {
			return
		}
		return
	}

	// Check for conflicts with existing unconfirmed transactions.
	if tp.conflict(t) {
		err = ConflictingTransactionErr
		return
	}

	// Check that the transaction is legal.
	err = tp.validTransaction(t)
	if err != nil {
		return
	}

	// Add the transaction.
	err = tp.addTransaction(t)
	if consensus.DEBUG {
		if err != nil {
			panic(err)
		}
	}

	return
}
