package core

import (
	"bytes"
	"errors"
	"math/big"
	"sync"

	"github.com/0xPolygon/go-ibft/messages/proto"
)

var (
	errVotingPowerNotCorrect = errors.New("total voting power is zero or less")
)

// ValidatorBackend defines an interface that has GetVotingPower and GetMinerAddress
type ValidatorBackend interface {
	// GetVotingPowers returns a map of validators' addresses and their voting powers for the specified height.
	GetVotingPowers(height uint64) (map[string]*big.Int, error)

	// GetMinerAddress returns the address of the miner.
	GetMinerAddress() (string, error)
}

// ValidatorManager keeps voting power and other information about validators
type ValidatorManager struct {
	vpLock *sync.RWMutex

	// quorumSize represents quorum for the height specified in the current View
	quorumSize *big.Int

	// validatorsVotingPower is a map of the validator addresses and their voting power for
	// the height specified in the current View
	validatorsVotingPower map[string]*big.Int

	// minerAddress stores the address of the miner
	minerAddress string

	backend ValidatorBackend

	log Logger
}

// NewValidatorManager creates a new ValidatorManager
func NewValidatorManager(backend ValidatorBackend, log Logger) (*ValidatorManager, error) {
	minerAddress, err := backend.GetMinerAddress()
	if err != nil {
		return nil, err
	}

	return &ValidatorManager{
		quorumSize:            big.NewInt(0),
		backend:               backend,
		validatorsVotingPower: nil,
		minerAddress:          minerAddress, // Store the miner address
		log:                   log,
		vpLock:                &sync.RWMutex{},
	}, nil
}

// Init sets voting power and quorum size
func (vm *ValidatorManager) Init(height uint64) error {
	validatorsVotingPower, err := vm.backend.GetVotingPowers(height)
	if err != nil {
		return err
	}

	return vm.setCurrentVotingPower(validatorsVotingPower)
}

// setCurrentVotingPower sets the current total voting power and quorum size
// based on current validators' voting power
func (vm *ValidatorManager) setCurrentVotingPower(validatorsVotingPower map[string]*big.Int) error {
	vm.vpLock.Lock()
	defer vm.vpLock.Unlock()

	totalVotingPower := calculateTotalVotingPower(validatorsVotingPower)
	if totalVotingPower.Cmp(big.NewInt(0)) <= 0 {
		return errVotingPowerNotCorrect
	}

	vm.validatorsVotingPower = validatorsVotingPower
	vm.quorumSize = calculateQuorum(totalVotingPower)

	return nil
}

// HasQuorum provides information on whether messages have reached the quorum
func (vm *ValidatorManager) HasQuorum(sendersAddrs map[string]struct{}) bool {
	vm.vpLock.RLock()
	defer vm.vpLock.RUnlock()

	// If not initialized correctly, return false
	if vm.validatorsVotingPower == nil {
		return false
	}

	messageVotePower := big.NewInt(0)

	for from := range sendersAddrs {
		if vote, ok := vm.validatorsVotingPower[from]; ok {
			messageVotePower.Add(messageVotePower, vote)
		}
	}

	// aggVotingPower >= (2 * totalVotingPower / 3) + 1
	return messageVotePower.Cmp(vm.quorumSize) >= 0
}

// HasPrepareQuorum provides information on whether prepared messages have reached the quorum
func (vm *ValidatorManager) HasPrepareQuorum(stateName stateType, proposalMessage *proto.Message,
	msgs []*proto.Message) bool {
	if proposalMessage == nil {
		// If the state is in the prepare phase, the proposal must be set. Otherwise, just return false since
		// this is a valid scenario, e.g., a proposal message is received before a prepare message for the same view
		if stateName == prepare {
			vm.log.Error("HasPrepareQuorum - proposalMessage is not set")
		}

		return false
	}

	proposerAddress := proposalMessage.From
	sendersAddressesMap := map[string]struct{}{
		string(proposerAddress): {},
	}

	for _, message := range msgs {
		if bytes.Equal(message.From, proposerAddress) {
			vm.log.Error("HasPrepareQuorum - proposer is among signers but it is not expected to be")

			return false
		}

		sendersAddressesMap[string(message.From)] = struct{}{}
	}

	return vm.HasQuorum(sendersAddressesMap)
}

// calculateQuorum calculates the quorum size, which is FLOOR(2 * totalVotingPower / 3) + 1
func calculateQuorum(totalVotingPower *big.Int) *big.Int {
	quorum := new(big.Int).Mul(totalVotingPower, big.NewInt(2))

	// This will floor the (2 * totalVotingPower/3) and add 1
	return quorum.Div(quorum, big.NewInt(3)).Add(quorum, big.NewInt(1))
}

func calculateTotalVotingPower(validatorsVotingPower map[string]*big.Int) *big.Int {
	totalVotingPower := big.NewInt(0)
	for _, validatorVotingPower := range validatorsVotingPower {
		totalVotingPower.Add(totalVotingPower, validatorVotingPower)
	}

	return totalVotingPower
}

// convertMessageToAddressSet converts a message slice to an addresses map
func convertMessageToAddressSet(messages []*proto.Message) map[string]struct{} {
	result := make(map[string]struct{}, len(messages))

	for _, x := range messages {
		result[string(x.From)] = struct{}{}
	}

	return result
}
