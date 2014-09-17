package dht

import (
	"testing"

	crand "crypto/rand"

	context "github.com/jbenet/go-ipfs/Godeps/_workspace/src/code.google.com/p/go.net/context"
	"github.com/jbenet/go-ipfs/Godeps/_workspace/src/code.google.com/p/goprotobuf/proto"

	ds "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/datastore.go"
	ma "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-multiaddr"
	msg "github.com/jbenet/go-ipfs/net/message"
	mux "github.com/jbenet/go-ipfs/net/mux"
	peer "github.com/jbenet/go-ipfs/peer"
	u "github.com/jbenet/go-ipfs/util"

	"time"
)

// fauxNet is a standin for a swarm.Network in order to more easily recreate
// different testing scenarios
type fauxSender struct {
	handlers []mesHandleFunc
}

func (f *fauxSender) SendRequest(ctx context.Context, m msg.NetMessage) (msg.NetMessage, error) {

	for _, h := range f.handlers {
		reply := h(m)
		if reply != nil {
			return reply, nil
		}
	}

	return nil, nil
}

func (f *fauxSender) SendMessage(ctx context.Context, m msg.NetMessage) error {
	for _, h := range f.handlers {
		reply := h(m)
		if reply != nil {
			return nil
		}
	}
	return nil
}

// fauxNet is a standin for a swarm.Network in order to more easily recreate
// different testing scenarios
type fauxNet struct {
	handlers []mesHandleFunc
}

// mesHandleFunc is a function that takes in outgoing messages
// and can respond to them, simulating other peers on the network.
// returning nil will chose not to respond and pass the message onto the
// next registered handler
type mesHandleFunc func(msg.NetMessage) msg.NetMessage

func (f *fauxNet) AddHandler(fn func(msg.NetMessage) msg.NetMessage) {
	f.handlers = append(f.handlers, fn)
}

// DialPeer attempts to establish a connection to a given peer
func (f *fauxNet) DialPeer(*peer.Peer) error {
	return nil
}

// ClosePeer connection to peer
func (f *fauxNet) ClosePeer(*peer.Peer) error {
	return nil
}

// IsConnected returns whether a connection to given peer exists.
func (f *fauxNet) IsConnected(*peer.Peer) (bool, error) {
	return true, nil
}

// GetProtocols returns the protocols registered in the network.
func (f *fauxNet) GetProtocols() *mux.ProtocolMap { return nil }

// SendMessage sends given Message out
func (f *fauxNet) SendMessage(msg.NetMessage) error {
	return nil
}

// Close terminates all network operation
func (f *fauxNet) Close() error { return nil }

func TestGetFailures(t *testing.T) {
	ctx := context.Background()
	fn := &fauxNet{}
	fs := &fauxSender{}

	peerstore := peer.NewPeerstore()
	local := new(peer.Peer)
	local.ID = peer.ID("test_peer")

	d := NewDHT(local, peerstore, fn, fs, ds.NewMapDatastore())

	other := &peer.Peer{ID: peer.ID("other_peer")}

	d.Start()

	d.Update(other)

	// This one should time out
	_, err := d.GetValue(u.Key("test"), time.Millisecond*10)
	if err != nil {
		if err != u.ErrTimeout {
			t.Fatal("Got different error than we expected.")
		}
	} else {
		t.Fatal("Did not get expected error!")
	}

	// Reply with failures to every message
	fn.AddHandler(func(mes msg.NetMessage) msg.NetMessage {
		pmes := new(Message)
		err := proto.Unmarshal(mes.Data(), pmes)
		if err != nil {
			t.Fatal(err)
		}

		resp := &Message{
			Type: pmes.Type,
		}
		m, err := msg.FromObject(mes.Peer(), resp)
		return m
	})

	// This one should fail with NotFound
	_, err = d.GetValue(u.Key("test"), time.Millisecond*1000)
	if err != nil {
		if err != u.ErrNotFound {
			t.Fatalf("Expected ErrNotFound, got: %s", err)
		}
	} else {
		t.Fatal("expected error, got none.")
	}

	success := make(chan struct{})
	fn.handlers = nil
	fn.AddHandler(func(mes msg.NetMessage) msg.NetMessage {
		resp := new(Message)
		err := proto.Unmarshal(mes.Data(), resp)
		if err != nil {
			t.Fatal(err)
		}
		success <- struct{}{}
		return nil
	})

	// Now we test this DHT's handleGetValue failure
	typ := Message_GET_VALUE
	str := "hello"
	req := Message{
		Type:  &typ,
		Key:   &str,
		Value: []byte{0},
	}

	mes, err := msg.FromObject(other, &req)
	if err != nil {
		t.Error(err)
	}

	mes, err = fs.SendRequest(ctx, mes)
	if err != nil {
		t.Error(err)
	}

	<-success
}

// TODO: Maybe put these in some sort of "ipfs_testutil" package
func _randPeer() *peer.Peer {
	p := new(peer.Peer)
	p.ID = make(peer.ID, 16)
	p.Addresses = []*ma.Multiaddr{nil}
	crand.Read(p.ID)
	return p
}

func TestNotFound(t *testing.T) {
	fn := &fauxNet{}
	fs := &fauxSender{}

	local := new(peer.Peer)
	local.ID = peer.ID("test_peer")
	peerstore := peer.NewPeerstore()

	d := NewDHT(local, peerstore, fn, fs, ds.NewMapDatastore())
	d.Start()

	var ps []*peer.Peer
	for i := 0; i < 5; i++ {
		ps = append(ps, _randPeer())
		d.Update(ps[i])
	}

	// Reply with random peers to every message
	fn.AddHandler(func(mes msg.NetMessage) msg.NetMessage {
		pmes := new(Message)
		err := proto.Unmarshal(mes.Data(), pmes)
		if err != nil {
			t.Fatal(err)
		}

		switch pmes.GetType() {
		case Message_GET_VALUE:
			resp := &Message{Type: pmes.Type}

			peers := []*peer.Peer{}
			for i := 0; i < 7; i++ {
				peers = append(peers, _randPeer())
			}
			resp.CloserPeers = peersToPBPeers(peers)
			mes, err := msg.FromObject(mes.Peer(), resp)
			if err != nil {
				t.Error(err)
			}
			return mes
		default:
			panic("Shouldnt recieve this.")
		}

	})

	_, err := d.GetValue(u.Key("hello"), time.Second*30)
	if err != nil {
		switch err {
		case u.ErrNotFound:
			//Success!
			return
		case u.ErrTimeout:
			t.Fatal("Should not have gotten timeout!")
		default:
			t.Fatalf("Got unexpected error: %s", err)
		}
	}
	t.Fatal("Expected to recieve an error.")
}

// If less than K nodes are in the entire network, it should fail when we make
// a GET rpc and nobody has the value
func TestLessThanKResponses(t *testing.T) {
	u.Debug = false
	fn := &fauxNet{}
	fs := &fauxSender{}
	peerstore := peer.NewPeerstore()
	local := new(peer.Peer)
	local.ID = peer.ID("test_peer")

	d := NewDHT(local, peerstore, fn, fs, ds.NewMapDatastore())
	d.Start()

	var ps []*peer.Peer
	for i := 0; i < 5; i++ {
		ps = append(ps, _randPeer())
		d.Update(ps[i])
	}
	other := _randPeer()

	// Reply with random peers to every message
	fn.AddHandler(func(mes msg.NetMessage) msg.NetMessage {
		pmes := new(Message)
		err := proto.Unmarshal(mes.Data(), pmes)
		if err != nil {
			t.Fatal(err)
		}

		switch pmes.GetType() {
		case Message_GET_VALUE:
			resp := &Message{
				Type:        pmes.Type,
				CloserPeers: peersToPBPeers([]*peer.Peer{other}),
			}

			mes, err := msg.FromObject(mes.Peer(), resp)
			if err != nil {
				t.Error(err)
			}
			return mes
		default:
			panic("Shouldnt recieve this.")
		}

	})

	_, err := d.GetValue(u.Key("hello"), time.Second*30)
	if err != nil {
		switch err {
		case u.ErrNotFound:
			//Success!
			return
		case u.ErrTimeout:
			t.Fatal("Should not have gotten timeout!")
		default:
			t.Fatalf("Got unexpected error: %s", err)
		}
	}
	t.Fatal("Expected to recieve an error.")
}
