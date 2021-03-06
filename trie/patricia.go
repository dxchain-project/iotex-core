package trie

import (
	"bytes"
	"container/list"
	"encoding/gob"

	"github.com/pkg/errors"
	"golang.org/x/crypto/blake2b"

	"github.com/iotexproject/iotex-core/common"
)

const RADIX = 256

var (
	// ErrInvalidPatricia: invalid operation
	ErrInvalidPatricia = errors.New("invalid patricia operation")

	// ErrPathDiverge: the path diverges
	ErrPathDiverge = errors.New("path diverges")
)

type (
	patricia interface {
		descend([]byte) ([]byte, int, error)
		ascend([]byte, byte) bool
		insert([]byte, []byte, *list.List) error
		increase([]byte) (int, int, int)
		collapse(byte, bool) ([]byte, []byte, bool)
		blob() ([]byte, error)
		hash() common.Hash32B // hash of this node
		serialize() ([]byte, error)
		deserialize([]byte) error
	}
	// key of next patricia node
	ptrcKey []byte
	// branch is the full node having 256 hashes for next level patricia node + hash of leaf node
	branch struct {
		Path  [RADIX]ptrcKey
		Value []byte
	}
	// leaf is squashed path + actual value (or hash of next patricia node for extension)
	leaf struct {
		Ext   byte // this is an extension node
		Path  ptrcKey
		Value []byte
	}
)

//======================================
// functions for branch
//======================================
// descend returns the key to retrieve next patricia, and length of matching path in bytes
func (b *branch) descend(key []byte) ([]byte, int, error) {
	node := b.Path[key[0]]
	if len(node) > 0 {
		return node, 1, nil
	}
	return nil, 0, errors.Wrapf(ErrInvalidPatricia, "branch does not have path = %d", key[0])
}

// ascend updates the key and returns whether the current node hash to be updated or not
func (b *branch) ascend(key []byte, index byte) bool {
	if b.Path[index] == nil {
		b.Path[index] = make([]byte, common.HashSize)
	}
	copy(b.Path[index], key)
	return true
}

// insert <key, value> at current patricia node
func (b *branch) insert(key, value []byte, stack *list.List) error {
	node := b.Path[key[0]]
	if len(node) > 0 {
		return errors.Wrapf(ErrInvalidPatricia, "branch already covers path = %d", key[0])
	}
	// create a new leaf
	l := leaf{0, key[1:], value}
	hashl := l.hash()
	b.Path[key[0]] = hashl[:]
	stack.PushBack(&l)
	return nil
}

// increase returns the number of nodes (B, E, L) being added as a result of insert()
func (b *branch) increase(key []byte) (int, int, int) {
	return 0, 0, 1
}

// collapse updates the node, returns the <key, value> if the node can be collapsed
func (b *branch) collapse(index byte, childCollapse bool) ([]byte, []byte, bool) {
	// if child cannot collapse, no need to check and return false
	if !childCollapse {
		return nil, nil, false
	}

	nb := 0
	var key, value []byte
	for i := 0; i < RADIX && i != int(index); i++ {
		if len(b.Path[i]) > 0 {
			nb++
			key = append(key, byte(i))
			value = b.Path[i]
		}
	}
	// branch can be collapsed if only 1 path remaining
	if nb == 1 {
		b.Path[index] = nil
		return key, value, true
	}
	return nil, nil, false
}

// blob return the value stored in the node
func (b *branch) blob() ([]byte, error) {
	// extension node stores the hash to next patricia node
	return nil, errors.Wrap(ErrInvalidPatricia, "branch does not store value")
}

// hash return the hash of this node
func (b *branch) hash() common.Hash32B {
	stream := []byte{}
	for i := 0; i < RADIX; i++ {
		stream = append(stream, b.Path[i]...)
	}
	stream = append(stream, b.Value...)
	return blake2b.Sum256(stream)
}

// serialize to bytes
func (b *branch) serialize() ([]byte, error) {
	var stream bytes.Buffer
	enc := gob.NewEncoder(&stream)
	if err := enc.Encode(b); err != nil {
		return nil, err
	}
	// first byte denotes the type of patricia: 2-branch, 1-extension, 0-leaf
	return append([]byte{2}, stream.Bytes()...), nil
}

// deserialize to branch
func (b *branch) deserialize(stream []byte) error {
	// reset variable
	*b = branch{}
	dec := gob.NewDecoder(bytes.NewBuffer(stream[1:]))
	if err := dec.Decode(b); err != nil {
		return err
	}
	return nil
}

//======================================
// functions for leaf
//======================================
// descend returns the key to retrieve next patricia, and length of matching path in bytes
func (l *leaf) descend(key []byte) ([]byte, int, error) {
	match := 0
	for l.Path[match] == key[match] {
		match++
		if match == len(l.Path) {
			return l.Value, match, nil
		}
	}
	return nil, match, ErrPathDiverge
}

// ascend updates the key and returns whether the current node hash to be updated or not
func (l *leaf) ascend(key []byte, index byte) bool {
	// leaf node will be replaced by newly created node, no need to update hash
	if l.Ext == 0 {
		return false
	}
	if l.Value == nil {
		l.Value = make([]byte, common.HashSize)
	}
	copy(l.Value, key)
	return true
}

// insert <key, value> at current patricia node
func (l *leaf) insert(key, value []byte, stack *list.List) error {
	if l.Ext == 1 {
		// TODO: insert for extension
		return nil
	}
	// get the matching length
	match := 0
	for l.Path[match] == key[match] {
		match++
	}
	// insert() gets called b/c path does not totally match so the below should not happen, but check anyway
	if match == len(l.Path) {
		return errors.Wrapf(ErrInvalidPatricia, "try to split a node with matching path = %x", l.Path)
	}
	// add 2 leaf, l1 is current node, l2 for new <key, value>
	l1 := leaf{0, l.Path[match+1:], l.Value}
	hashl1 := l1.hash()
	l2 := leaf{0, key[match+1:], value}
	hashl2 := l2.hash()
	// add 1 branch to link 2 new leaf
	b := branch{}
	b.Path[l.Path[match]] = hashl1[:]
	b.Path[key[match]] = hashl2[:]
	// if there's matching part, add 1 ext leading to new branch
	if match > 0 {
		hashb := b.hash()
		e := leaf{1, key[:match], hashb[:]}
		stack.PushBack(&e)
	}
	stack.PushBack(&b)
	stack.PushBack(&l1)
	stack.PushBack(&l2)
	return nil
}

// increase returns the number of nodes (B, E, L) being added as a result of insert()
func (l *leaf) increase(key []byte) (int, int, int) {
	// get the matching length
	match := 0
	for l.Path[match] == key[match] {
		match++
	}
	if match > 0 {
		return 1, 1, 2
	}
	return 1, 0, 2
}

// collapse updates the node, returns the <key, value> if the node can be collapsed
func (l *leaf) collapse(index byte, childCollapse bool) ([]byte, []byte, bool) {
	// if child cannot collapse, no need to check and return false
	if !childCollapse {
		return nil, nil, false
	}
	return l.Path, l.Value, true
}

// blob return the value stored in the node
func (l *leaf) blob() ([]byte, error) {
	if l.Ext == 1 {
		// extension node stores the hash to next patricia node
		return nil, errors.Wrap(ErrInvalidPatricia, "extension does not store value")
	}
	return l.Value, nil
}

// hash return the hash of this node
func (l *leaf) hash() common.Hash32B {
	stream := append([]byte{l.Ext}, l.Path...)
	stream = append(stream, l.Value...)
	return blake2b.Sum256(stream)
}

// serialize to bytes
func (l *leaf) serialize() ([]byte, error) {
	stream := bytes.Buffer{}
	enc := gob.NewEncoder(&stream)
	if err := enc.Encode(l); err != nil {
		return nil, err
	}
	// first byte denotes the type of patricia: 2-branch, 1-extension, 0-leaf
	return append([]byte{l.Ext}, stream.Bytes()...), nil
}

// deserialize to leaf
func (l *leaf) deserialize(stream []byte) error {
	// reset variable
	*l = leaf{}
	dec := gob.NewDecoder(bytes.NewBuffer(stream[1:]))
	if err := dec.Decode(l); err != nil {
		return err
	}
	return nil
}
