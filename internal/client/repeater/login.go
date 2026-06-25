package repeater

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

type LoginResult struct {
	Success bool   `json:"success"`
	IsAdmin bool   `json:"isAdmin"`
	Role    string `json:"role,omitempty"`
}

func (rm *Client) SendLogin(pubkeyHex, password string, timeout time.Duration) (*LoginResult, error) {
	return rm.sendLogin(pubkeyHex, password, nil, timeout)
}

// SendRoomLogin logs in to a room server — same flow as SendLogin, but the
// plaintext carries a sync_since cursor (the server pushes posts newer than it).
func (rm *Client) SendRoomLogin(pubkeyHex, password string, syncSince uint32, timeout time.Duration) (*LoginResult, error) {
	return rm.sendLogin(pubkeyHex, password, &syncSince, timeout)
}

func (rm *Client) sendLogin(pubkeyHex, password string, roomSyncSince *uint32, timeout time.Duration) (*LoginResult, error) {
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid pubkey hex: %w", err)
	}

	peerIdentity, err := meshcore.NewIdentityFromBytes(pubkeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid pubkey: %w", err)
	}

	peer := rm.node.Peers().Lookup(peerIdentity.PublicKey())
	if peer == nil {
		return nil, fmt.Errorf("peer not found in peer table")
	}

	// Use the companion's static identity rather than a throwaway ephemeral key
	// so the repeater registers us in its ACL under our real pubkey. This lets
	// the firmware accept blank-password reauth on subsequent logins (its
	// `getClient(sender.pub_key)` lookup) and avoids accumulating orphan
	// contact entries on the repeater.
	selfIdentity := rm.node.Identity()
	selfSeed := selfIdentity.Seed()

	sharedSecret, err := meshcore.DeriveSharedSecret(selfSeed[:], peerIdentity.PublicKeyBytes())
	if err != nil {
		return nil, fmt.Errorf("deriving shared secret: %w", err)
	}

	var plaintext []byte
	if roomSyncSince != nil {
		// Room login: [timestamp:4][sync_since:4][password:N]
		plaintext = make([]byte, 8+len(password))
		binary.LittleEndian.PutUint32(plaintext[:4], uint32(time.Now().Unix()))
		binary.LittleEndian.PutUint32(plaintext[4:8], *roomSyncSince)
		copy(plaintext[8:], password)
	} else {
		// Repeater login: [timestamp:4][password:N]
		plaintext = make([]byte, 4+len(password))
		binary.LittleEndian.PutUint32(plaintext[:4], uint32(time.Now().Unix()))
		copy(plaintext[4:], password)
	}

	encrypted, err := meshcore.EncryptThenMAC(sharedSecret, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypting login: %w", err)
	}

	var mac [2]byte
	copy(mac[:], encrypted[:2])

	anonReq := &meshcore.AnonReq{
		Destination:      peerIdentity.PublicKey()[0],
		EphemeralPubKey:  selfIdentity.PublicKey(),
		MAC:              mac,
		EncryptedPayload: encrypted[2:],
	}

	payload, err := anonReq.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encoding anon req: %w", err)
	}

	resultCh := make(chan []byte, 1)
	pl := &pendingLogin{
		ch:             resultCh,
		created:        time.Now(),
		sharedSecret:   sharedSecret,
		peerPubKeyByte: peerIdentity.PublicKey()[0],
		peerPubKey:     peerIdentity.PublicKey(),
	}
	rm.loginMu.Lock()
	rm.pendingLogins = append(rm.pendingLogins, pl)
	rm.loginMu.Unlock()

	defer func() {
		rm.loginMu.Lock()
		for i, p := range rm.pendingLogins {
			if p == pl {
				rm.pendingLogins = append(rm.pendingLogins[:i], rm.pendingLogins[i+1:]...)
				break
			}
		}
		rm.loginMu.Unlock()
	}()

	routeType, pathLen := routeForPeer(peer)

	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeAnonReq, 0),
		PathLength: pathLen,
		Path:       peer.OutPath,
		Payload:    payload,
	}

	if err := rm.node.SendPacket(pkt); err != nil {
		return nil, fmt.Errorf("sending login: %w", err)
	}

	rm.log.Debug("login sent", "peer", pubkeyHex[:12])

	select {
	case data := <-resultCh:
		isAdmin := len(data) > 6 && data[6] == 1
		role := ""
		if roomSyncSince != nil {
			// Room login response byte 6: 1=admin, 2=read-only (guest), 0=read-write
			role = "read-write"
			if len(data) > 6 {
				switch data[6] {
				case 1:
					role = "admin"
				case 2:
					role = "read-only"
				}
			}
		}
		rm.mu.Lock()
		rm.sessions[pubkeyHex] = &Session{
			PubKeyHex:    pubkeyHex,
			IsAdmin:      isAdmin,
			Role:         role,
			IsRoom:       roomSyncSince != nil,
			LoggedInAt:   time.Now(),
			sharedSecret: sharedSecret,
			localPubKey:  selfIdentity.PublicKey(),
		}
		rm.mu.Unlock()
		return &LoginResult{Success: true, IsAdmin: isAdmin, Role: role}, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("login timed out")
	}
}
