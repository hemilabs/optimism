package node_utils

import (
	"fmt"
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/optimism/op-chain-ops/devkeys"
	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/dsl"
	devpresets "github.com/ethereum-optimism/optimism/op-devstack/presets"
	"github.com/ethereum-optimism/optimism/op-devstack/sysgo"
)

type L2NodeConfig struct {
	OpSequencerNodesWithGeth   int
	OpSequencerNodesWithReth   int
	KonaSequencerNodesWithGeth int
	KonaSequencerNodesWithReth int
	OpNodesWithGeth            int
	OpNodesWithReth            int
	KonaNodesWithGeth          int
	KonaNodesWithReth          int
}

const (
	DefaultOpSequencerGeth = 0
	DefaultOpSequencerReth = 0

	DefaultKonaSequencerGeth = 0
	DefaultKonaSequencerReth = 1

	DefaultOpValidatorGeth = 0
	DefaultOpValidatorReth = 0

	DefaultKonaValidatorGeth = 3
	DefaultKonaValidatorReth = 3
)

func ParseL2NodeConfigFromEnv() L2NodeConfig {
	opSequencerGethInt := parseEnvInt("OP_SEQUENCER_WITH_GETH", DefaultOpSequencerGeth)
	konaSequencerGethInt := parseEnvInt("KONA_SEQUENCER_WITH_GETH", DefaultKonaSequencerGeth)
	opSequencerRethInt := parseEnvInt("OP_SEQUENCER_WITH_RETH", DefaultOpSequencerReth)
	konaSequencerRethInt := parseEnvInt("KONA_SEQUENCER_WITH_RETH", DefaultKonaSequencerReth)
	opValidatorGethInt := parseEnvInt("OP_VALIDATOR_WITH_GETH", DefaultOpValidatorGeth)
	opValidatorRethInt := parseEnvInt("OP_VALIDATOR_WITH_RETH", DefaultOpValidatorReth)
	konaValidatorGethInt := parseEnvInt("KONA_VALIDATOR_WITH_GETH", DefaultKonaValidatorGeth)
	konaValidatorRethInt := parseEnvInt("KONA_VALIDATOR_WITH_RETH", DefaultKonaValidatorReth)

	return L2NodeConfig{
		OpSequencerNodesWithGeth:   opSequencerGethInt,
		OpSequencerNodesWithReth:   opSequencerRethInt,
		OpNodesWithGeth:            opValidatorGethInt,
		OpNodesWithReth:            opValidatorRethInt,
		KonaSequencerNodesWithGeth: konaSequencerGethInt,
		KonaSequencerNodesWithReth: konaSequencerRethInt,
		KonaNodesWithGeth:          konaValidatorGethInt,
		KonaNodesWithReth:          konaValidatorRethInt,
	}
}

func parseEnvInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func (l2NodeConfig L2NodeConfig) TotalNodes() int {
	return l2NodeConfig.OpSequencerNodesWithGeth + l2NodeConfig.OpSequencerNodesWithReth + l2NodeConfig.KonaSequencerNodesWithGeth + l2NodeConfig.KonaSequencerNodesWithReth + l2NodeConfig.OpNodesWithGeth + l2NodeConfig.OpNodesWithReth + l2NodeConfig.KonaNodesWithGeth + l2NodeConfig.KonaNodesWithReth
}

func (l2NodeConfig L2NodeConfig) OpSequencerNodes() int {
	return l2NodeConfig.OpSequencerNodesWithGeth + l2NodeConfig.OpSequencerNodesWithReth
}

func (l2NodeConfig L2NodeConfig) KonaSequencerNodes() int {
	return l2NodeConfig.KonaSequencerNodesWithGeth + l2NodeConfig.KonaSequencerNodesWithReth
}

func (l2NodeConfig L2NodeConfig) OpValidatorNodes() int {
	return l2NodeConfig.OpNodesWithGeth + l2NodeConfig.OpNodesWithReth
}

func (l2NodeConfig L2NodeConfig) KonaValidatorNodes() int {
	return l2NodeConfig.KonaNodesWithGeth + l2NodeConfig.KonaNodesWithReth
}

type MixedOpKonaPreset struct {
	Log log.Logger
	T   devtest.T

	L1Network *dsl.L1Network
	L1EL      *dsl.L1ELNode
	L1CL      *dsl.L1CLNode

	L2Chain   *dsl.L2Network
	L2Batcher *dsl.L2Batcher

	L2ELKonaSequencerNodes []dsl.L2ELNode
	L2CLKonaSequencerNodes []dsl.L2CLNode

	L2ELOpSequencerNodes []dsl.L2ELNode
	L2CLOpSequencerNodes []dsl.L2CLNode

	L2ELOpValidatorNodes []dsl.L2ELNode
	L2CLOpValidatorNodes []dsl.L2CLNode

	L2ELKonaValidatorNodes []dsl.L2ELNode
	L2CLKonaValidatorNodes []dsl.L2CLNode

	Wallet *dsl.HDWallet

	FaucetL1 *dsl.Faucet
	Faucet   *dsl.Faucet
	FunderL1 *dsl.Funder
	Funder   *dsl.Funder
}

func (m *MixedOpKonaPreset) L2ELNodes() []dsl.L2ELNode {
	return append(m.L2ELSequencerNodes(), m.L2ELValidatorNodes()...)
}

func (m *MixedOpKonaPreset) L2CLNodes() []dsl.L2CLNode {
	return append(m.L2CLSequencerNodes(), m.L2CLValidatorNodes()...)
}

func (m *MixedOpKonaPreset) L2CLValidatorNodes() []dsl.L2CLNode {
	return append(m.L2CLOpValidatorNodes, m.L2CLKonaValidatorNodes...)
}

func (m *MixedOpKonaPreset) L2CLSequencerNodes() []dsl.L2CLNode {
	return append(m.L2CLOpSequencerNodes, m.L2CLKonaSequencerNodes...)
}

func (m *MixedOpKonaPreset) L2ELValidatorNodes() []dsl.L2ELNode {
	return append(m.L2ELOpValidatorNodes, m.L2ELKonaValidatorNodes...)
}

func (m *MixedOpKonaPreset) L2ELSequencerNodes() []dsl.L2ELNode {
	return append(m.L2ELOpSequencerNodes, m.L2ELKonaSequencerNodes...)
}

func (m *MixedOpKonaPreset) L2CLKonaNodes() []dsl.L2CLNode {
	return append(m.L2CLKonaValidatorNodes, m.L2CLKonaSequencerNodes...)
}

func L2NodeMatcher[E stack.Identifiable](value ...string) stack.Matcher[E] {
	return match.MatchElemFn[E](func(elem E) bool {
		for _, v := range value {
			if !strings.Contains(elem.ID().Key(), v) {
				return false
			}
		}
		return true
	})
}

func (m *MixedOpKonaPreset) L2Networks() []*dsl.L2Network {
	return []*dsl.L2Network{m.L2Chain}
}

func NewMixedOpKona(t devtest.T) *MixedOpKonaPreset {
	return NewMixedOpKonaForConfig(t, ParseL2NodeConfigFromEnv())
}

func NewMixedOpKonaForConfig(t devtest.T, l2NodeConfig L2NodeConfig) *MixedOpKonaPreset {
	runtime := sysgo.NewMixedSingleChainRuntime(t, sysgo.MixedSingleChainPresetConfig{
		NodeSpecs: mixedOpKonaNodeSpecs(l2NodeConfig),
	})
	return NewMixedOpKonaFromRuntime(t, runtime)
}

	l1Net := system.L1Network(match.FirstL1Network)
	l2Net := system.L2Network(match.Assume(t, match.L2ChainA))

	t.Gate().GreaterOrEqual(len(l2Net.L2CLNodes()), 2, "expected at least two L2CL nodes")

	opSequencerCLNodes := L2NodeMatcher[stack.L2CLNode](string(OpNode), string(Sequencer)).Match(l2Net.L2CLNodes())
	konaSequencerCLNodes := L2NodeMatcher[stack.L2CLNode](string(KonaNode), string(Sequencer)).Match(l2Net.L2CLNodes())

	opCLNodes := L2NodeMatcher[stack.L2CLNode](string(OpNode), string(Validator)).Match(l2Net.L2CLNodes())
	konaCLNodes := L2NodeMatcher[stack.L2CLNode](string(KonaNode), string(Validator)).Match(l2Net.L2CLNodes())

	opSequencerELNodes := L2NodeMatcher[stack.L2ELNode](string(OpNode), string(Sequencer)).Match(l2Net.L2ELNodes())
	konaSequencerELNodes := L2NodeMatcher[stack.L2ELNode](string(KonaNode), string(Sequencer)).Match(l2Net.L2ELNodes())
	opELNodes := L2NodeMatcher[stack.L2ELNode](string(OpNode), string(Validator)).Match(l2Net.L2ELNodes())
	konaELNodes := L2NodeMatcher[stack.L2ELNode](string(KonaNode), string(Validator)).Match(l2Net.L2ELNodes())

func mixedOpKonaFromRuntime(t devtest.T, runtime *sysgo.MixedSingleChainRuntime) (*MixedOpKonaPreset, *devpresets.MixedSingleChainFrontends) {
	frontends := devpresets.NewMixedSingleChainFrontends(t, runtime)
	t.Gate().GreaterOrEqual(len(frontends.Nodes), 2, "expected at least two mixed L2 nodes")
	out := &MixedOpKonaPreset{
		Log:       t.Logger(),
		T:         t,
		L1Network: frontends.L1Network,
		L1EL:      frontends.L1EL,
		L1CL:      frontends.L1CL,
		L2Chain:   frontends.L2Network,
		L2Batcher: frontends.L2Batcher,
		Wallet:    dsl.NewHDWallet(t, devkeys.TestMnemonic, 30),
		FaucetL1:  frontends.FaucetL1,
		Faucet:    frontends.FaucetL2,
	}
	for _, node := range frontends.Nodes {
		switch {
		case node.Spec.CLKind == sysgo.MixedL2CLOpNode && node.Spec.IsSequencer:
			out.L2ELOpSequencerNodes = append(out.L2ELOpSequencerNodes, *node.EL)
			out.L2CLOpSequencerNodes = append(out.L2CLOpSequencerNodes, *node.CL)
		case node.Spec.CLKind == sysgo.MixedL2CLOpNode && !node.Spec.IsSequencer:
			out.L2ELOpValidatorNodes = append(out.L2ELOpValidatorNodes, *node.EL)
			out.L2CLOpValidatorNodes = append(out.L2CLOpValidatorNodes, *node.CL)
		case node.Spec.CLKind == sysgo.MixedL2CLKona && node.Spec.IsSequencer:
			out.L2ELKonaSequencerNodes = append(out.L2ELKonaSequencerNodes, *node.EL)
			out.L2CLKonaSequencerNodes = append(out.L2CLKonaSequencerNodes, *node.CL)
		case node.Spec.CLKind == sysgo.MixedL2CLKona && !node.Spec.IsSequencer:
			out.L2ELKonaValidatorNodes = append(out.L2ELKonaValidatorNodes, *node.EL)
			out.L2CLKonaValidatorNodes = append(out.L2CLKonaValidatorNodes, *node.CL)
		}
		if out.Funder == nil && node.Spec.IsSequencer {
			out.Funder = dsl.NewFunder(out.Wallet, out.Faucet, node.EL)
		}
	}
	out.FunderL1 = dsl.NewFunder(out.Wallet, out.FaucetL1, out.L1EL)
	return out, frontends
}

type DefaultMixedOpKonaSystemIDs struct {
	L1   stack.ComponentID
	L1EL stack.ComponentID
	L1CL stack.ComponentID

	L2 stack.ComponentID

	L2ELOpGethSequencerNodes []stack.ComponentID
	L2ELOpRethSequencerNodes []stack.ComponentID

	L2CLOpGethSequencerNodes []stack.ComponentID
	L2CLOpRethSequencerNodes []stack.ComponentID

	L2ELKonaGethSequencerNodes []stack.ComponentID
	L2ELKonaRethSequencerNodes []stack.ComponentID

	L2CLKonaGethSequencerNodes []stack.ComponentID
	L2CLKonaRethSequencerNodes []stack.ComponentID

	L2CLOpGethNodes []stack.ComponentID
	L2ELOpGethNodes []stack.ComponentID

	L2CLOpRethNodes []stack.ComponentID
	L2ELOpRethNodes []stack.ComponentID

	L2CLKonaGethNodes []stack.ComponentID
	L2ELKonaGethNodes []stack.ComponentID

	L2CLKonaRethNodes []stack.ComponentID
	L2ELKonaRethNodes []stack.ComponentID

	L2Batcher  stack.ComponentID
	L2Proposer stack.ComponentID
}

func (ids *DefaultMixedOpKonaSystemIDs) L2CLSequencerNodes() []stack.ComponentID {
	list := append(ids.L2CLOpGethSequencerNodes, ids.L2CLOpRethSequencerNodes...)
	list = append(list, ids.L2CLKonaGethSequencerNodes...)
	list = append(list, ids.L2CLKonaRethSequencerNodes...)
	return list
}

func (ids *DefaultMixedOpKonaSystemIDs) L2ELSequencerNodes() []stack.ComponentID {
	list := append(ids.L2ELOpGethSequencerNodes, ids.L2ELOpRethSequencerNodes...)
	list = append(list, ids.L2ELKonaGethSequencerNodes...)
	list = append(list, ids.L2ELKonaRethSequencerNodes...)
	return list
}

func (ids *DefaultMixedOpKonaSystemIDs) L2CLValidatorNodes() []stack.ComponentID {
	list := append(ids.L2CLOpGethNodes, ids.L2CLOpRethNodes...)
	list = append(list, ids.L2CLKonaGethNodes...)
	list = append(list, ids.L2CLKonaRethNodes...)
	return list
}
func (ids *DefaultMixedOpKonaSystemIDs) L2ELValidatorNodes() []stack.ComponentID {
	list := append(ids.L2ELOpGethNodes, ids.L2ELOpRethNodes...)
	list = append(list, ids.L2ELKonaGethNodes...)
	list = append(list, ids.L2ELKonaRethNodes...)
	return list
}

func (ids *DefaultMixedOpKonaSystemIDs) L2CLNodes() []stack.ComponentID {
	return append(ids.L2CLSequencerNodes(), ids.L2CLValidatorNodes()...)
}

func (ids *DefaultMixedOpKonaSystemIDs) L2ELNodes() []stack.ComponentID {
	return append(ids.L2ELSequencerNodes(), ids.L2ELValidatorNodes()...)
}

func NewDefaultMixedOpKonaSystemIDs(l1ID, l2ID eth.ChainID, l2NodeConfig L2NodeConfig) DefaultMixedOpKonaSystemIDs {
	rethOpCLNodes := make([]stack.ComponentID, l2NodeConfig.OpNodesWithReth)
	rethOpELNodes := make([]stack.ComponentID, l2NodeConfig.OpNodesWithReth)
	rethKonaCLNodes := make([]stack.ComponentID, l2NodeConfig.KonaNodesWithReth)
	rethKonaELNodes := make([]stack.ComponentID, l2NodeConfig.KonaNodesWithReth)

	gethOpCLNodes := make([]stack.ComponentID, l2NodeConfig.OpNodesWithGeth)
	gethOpELNodes := make([]stack.ComponentID, l2NodeConfig.OpNodesWithGeth)
	gethKonaCLNodes := make([]stack.ComponentID, l2NodeConfig.KonaNodesWithGeth)
	gethKonaELNodes := make([]stack.ComponentID, l2NodeConfig.KonaNodesWithGeth)

	gethOpSequencerCLNodes := make([]stack.ComponentID, l2NodeConfig.OpSequencerNodesWithGeth)
	gethOpSequencerELNodes := make([]stack.ComponentID, l2NodeConfig.OpSequencerNodesWithGeth)
	gethKonaSequencerCLNodes := make([]stack.ComponentID, l2NodeConfig.KonaSequencerNodesWithGeth)
	gethKonaSequencerELNodes := make([]stack.ComponentID, l2NodeConfig.KonaSequencerNodesWithGeth)

	rethOpSequencerCLNodes := make([]stack.ComponentID, l2NodeConfig.OpSequencerNodesWithReth)
	rethOpSequencerELNodes := make([]stack.ComponentID, l2NodeConfig.OpSequencerNodesWithReth)
	rethKonaSequencerCLNodes := make([]stack.ComponentID, l2NodeConfig.KonaSequencerNodesWithReth)
	rethKonaSequencerELNodes := make([]stack.ComponentID, l2NodeConfig.KonaSequencerNodesWithReth)

	for i := range l2NodeConfig.OpSequencerNodesWithGeth {
		gethOpSequencerCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-geth-op-sequencer-%d", i), l2ID)
		gethOpSequencerELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-geth-op-sequencer-%d", i), l2ID)
	}

	for i := range l2NodeConfig.KonaSequencerNodesWithGeth {
		gethKonaSequencerCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-geth-kona-sequencer-%d", i), l2ID)
		gethKonaSequencerELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-geth-kona-sequencer-%d", i), l2ID)
	}

	for i := range l2NodeConfig.OpSequencerNodesWithReth {
		rethOpSequencerCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-reth-op-sequencer-%d", i), l2ID)
		rethOpSequencerELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-reth-op-sequencer-%d", i), l2ID)
	}

	for i := range l2NodeConfig.KonaSequencerNodesWithReth {
		rethKonaSequencerCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-reth-kona-sequencer-%d", i), l2ID)
		rethKonaSequencerELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-reth-kona-sequencer-%d", i), l2ID)
	}

	for i := range l2NodeConfig.OpNodesWithGeth {
		gethOpCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-geth-op-validator-%d", i), l2ID)
		gethOpELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-geth-op-validator-%d", i), l2ID)
	}

	for i := range l2NodeConfig.OpNodesWithReth {
		rethOpCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-reth-op-validator-%d", i), l2ID)
		rethOpELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-reth-op-validator-%d", i), l2ID)
	}

	for i := range l2NodeConfig.KonaNodesWithGeth {
		gethKonaCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-geth-kona-validator-%d", i), l2ID)
		gethKonaELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-geth-kona-validator-%d", i), l2ID)
	}

	for i := range l2NodeConfig.KonaNodesWithReth {
		rethKonaCLNodes[i] = stack.NewL2CLNodeID(fmt.Sprintf("cl-reth-kona-validator-%d", i), l2ID)
		rethKonaELNodes[i] = stack.NewL2ELNodeID(fmt.Sprintf("el-reth-kona-validator-%d", i), l2ID)
	}

	ids := DefaultMixedOpKonaSystemIDs{
		L1:   stack.NewL1NetworkID(l1ID),
		L1EL: stack.NewL1ELNodeID("l1", l1ID),
		L1CL: stack.NewL1CLNodeID("l1", l1ID),
		L2:   stack.NewL2NetworkID(l2ID),

		L2CLOpGethSequencerNodes: gethOpSequencerCLNodes,
		L2ELOpGethSequencerNodes: gethOpSequencerELNodes,

		L2CLOpRethSequencerNodes: rethOpSequencerCLNodes,
		L2ELOpRethSequencerNodes: rethOpSequencerELNodes,

		L2CLOpGethNodes: gethOpCLNodes,
		L2ELOpGethNodes: gethOpELNodes,

		L2CLOpRethNodes: rethOpCLNodes,
		L2ELOpRethNodes: rethOpELNodes,

		L2CLKonaGethSequencerNodes: gethKonaSequencerCLNodes,
		L2ELKonaGethSequencerNodes: gethKonaSequencerELNodes,

		L2CLKonaRethSequencerNodes: rethKonaSequencerCLNodes,
		L2ELKonaRethSequencerNodes: rethKonaSequencerELNodes,

		L2CLKonaGethNodes: gethKonaCLNodes,
		L2ELKonaGethNodes: gethKonaELNodes,

		L2CLKonaRethNodes: rethKonaCLNodes,
		L2ELKonaRethNodes: rethKonaELNodes,

		L2Batcher:  stack.NewL2BatcherID("main", l2ID),
		L2Proposer: stack.NewL2ProposerID("main", l2ID),
	}
	return ids
}

func DefaultMixedOpKonaSystem(dest *DefaultMixedOpKonaSystemIDs, l2NodeConfig L2NodeConfig) stack.CombinedOption[*sysgo.Orchestrator] {
	l1ID := eth.ChainIDFromUInt64(DefaultL1ID)
	l2ID := eth.ChainIDFromUInt64(DefaultL2ID)
	ids := NewDefaultMixedOpKonaSystemIDs(l1ID, l2ID, l2NodeConfig)

	opt := stack.Combine[*sysgo.Orchestrator]()
	opt.Add(stack.BeforeDeploy(func(o *sysgo.Orchestrator) {
		o.P().Logger().Info("Setting up")
	}))

	opt.Add(sysgo.WithMnemonicKeys(devkeys.TestMnemonic))

	opt.Add(sysgo.WithDeployer(),
		sysgo.WithDeployerOptions(
			sysgo.WithLocalContractSources(),
			sysgo.WithCommons(ids.L1.ChainID()),
			sysgo.WithPrefundedL2(ids.L1.ChainID(), ids.L2.ChainID()),
		),
	)

	opt.Add(sysgo.WithL1Nodes(ids.L1EL, ids.L1CL))

	// Spawn all nodes.
	for i := range ids.L2CLKonaGethSequencerNodes {
		opt.Add(sysgo.WithOpGeth(ids.L2ELKonaGethSequencerNodes[i]))
		opt.Add(sysgo.WithKonaNode(ids.L2CLKonaGethSequencerNodes[i], ids.L1CL, ids.L1EL, ids.L2ELKonaGethSequencerNodes[i], sysgo.L2CLOptionFn(func(p devtest.P, id stack.ComponentID, cfg *sysgo.L2CLConfig) {
			cfg.IsSequencer = true
			cfg.SequencerSyncMode = sync.ELSync
			cfg.VerifierSyncMode = sync.ELSync
		})))
	}

	for i := range ids.L2CLOpGethSequencerNodes {
		opt.Add(sysgo.WithOpGeth(ids.L2ELOpGethSequencerNodes[i]))
		opt.Add(sysgo.WithOpNode(ids.L2CLOpGethSequencerNodes[i], ids.L1CL, ids.L1EL, ids.L2ELOpGethSequencerNodes[i], sysgo.L2CLOptionFn(func(p devtest.P, id stack.ComponentID, cfg *sysgo.L2CLConfig) {
			cfg.IsSequencer = true
		})))
	}

	for i := range ids.L2CLKonaRethSequencerNodes {
		opt.Add(sysgo.WithOpReth(ids.L2ELKonaRethSequencerNodes[i]))
		opt.Add(sysgo.WithKonaNode(ids.L2CLKonaRethSequencerNodes[i], ids.L1CL, ids.L1EL, ids.L2ELKonaRethSequencerNodes[i], sysgo.L2CLOptionFn(func(p devtest.P, id stack.ComponentID, cfg *sysgo.L2CLConfig) {
			cfg.IsSequencer = true
			cfg.SequencerSyncMode = sync.ELSync
			cfg.VerifierSyncMode = sync.ELSync
		})))
	}

	for i := range ids.L2CLOpRethSequencerNodes {
		opt.Add(sysgo.WithOpReth(ids.L2ELOpRethSequencerNodes[i]))
		opt.Add(sysgo.WithOpNode(ids.L2CLOpRethSequencerNodes[i], ids.L1CL, ids.L1EL, ids.L2ELOpRethSequencerNodes[i], sysgo.L2CLOptionFn(func(p devtest.P, id stack.ComponentID, cfg *sysgo.L2CLConfig) {
			cfg.IsSequencer = true
		})))
	}

	for i := range ids.L2CLKonaGethNodes {
		opt.Add(sysgo.WithOpGeth(ids.L2ELKonaGethNodes[i]))
		opt.Add(sysgo.WithKonaNode(ids.L2CLKonaGethNodes[i], ids.L1CL, ids.L1EL, ids.L2ELKonaGethNodes[i], sysgo.L2CLOptionFn(func(p devtest.P, id stack.ComponentID, cfg *sysgo.L2CLConfig) {
			cfg.SequencerSyncMode = sync.ELSync
			cfg.VerifierSyncMode = sync.ELSync
		})))
	}

	for i := range ids.L2ELOpGethNodes {
		opt.Add(sysgo.WithOpGeth(ids.L2ELOpGethNodes[i]))
		opt.Add(sysgo.WithOpNode(ids.L2CLOpGethNodes[i], ids.L1CL, ids.L1EL, ids.L2ELOpGethNodes[i]))
	}

	for i := range ids.L2CLKonaRethNodes {
		opt.Add(sysgo.WithOpReth(ids.L2ELKonaRethNodes[i]))
		opt.Add(sysgo.WithKonaNode(ids.L2CLKonaRethNodes[i], ids.L1CL, ids.L1EL, ids.L2ELKonaRethNodes[i], sysgo.L2CLOptionFn(func(p devtest.P, id stack.ComponentID, cfg *sysgo.L2CLConfig) {
			cfg.SequencerSyncMode = sync.ELSync
			cfg.VerifierSyncMode = sync.ELSync
		})))
	}

	for i := range ids.L2ELOpRethNodes {
		opt.Add(sysgo.WithOpReth(ids.L2ELOpRethNodes[i]))
		opt.Add(sysgo.WithOpNode(ids.L2CLOpRethNodes[i], ids.L1CL, ids.L1EL, ids.L2ELOpRethNodes[i]))
	}

	// Connect all nodes to each other in the p2p network.
	CLNodeIDs := ids.L2CLNodes()
	ELNodeIDs := ids.L2ELNodes()

	for i := range CLNodeIDs {
		for j := range i {
			opt.Add(sysgo.WithL2CLP2PConnection(CLNodeIDs[i], CLNodeIDs[j]))
			opt.Add(sysgo.WithL2ELP2PConnection(ELNodeIDs[i], ELNodeIDs[j], false))
		}
	}

	appendSpecs(cfg.OpSequencerNodesWithGeth, "el-geth-op-sequencer", "cl-geth-op-sequencer", sysgo.MixedL2ELOpGeth, sysgo.MixedL2CLOpNode, true)
	appendSpecs(cfg.OpSequencerNodesWithReth, "el-reth-op-sequencer", "cl-reth-op-sequencer", sysgo.MixedL2ELOpReth, sysgo.MixedL2CLOpNode, true)
	appendSpecs(cfg.KonaSequencerNodesWithGeth, "el-geth-kona-sequencer", "cl-geth-kona-sequencer", sysgo.MixedL2ELOpGeth, sysgo.MixedL2CLKona, true)
	appendSpecs(cfg.KonaSequencerNodesWithReth, "el-reth-kona-sequencer", "cl-reth-kona-sequencer", sysgo.MixedL2ELOpReth, sysgo.MixedL2CLKona, true)

	opt.Add(sysgo.WithFaucets([]stack.ComponentID{ids.L1EL}, []stack.ComponentID{ELNodeIDs[0]}))

	return specs
}
