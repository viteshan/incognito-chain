package peer

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/incognitochain/incognito-chain/common"
	"github.com/incognitochain/incognito-chain/wire"
	"github.com/libp2p/go-libp2p-peer"
)

type PeerConn struct {
	connState      ConnState
	stateMtx       sync.RWMutex
	verAckReceived bool

	// channel
	sendMessageQueue chan outMsg
	cDisconnect      chan struct{}
	cRead            chan struct{}
	cWrite           chan struct{}
	cClose           chan struct{}
	cMsgHash         map[string]chan bool

	RetryCount int32

	// remote peer info
	RemotePeer       *Peer
	RemotePeerID     peer.ID
	RemoteRawAddress string
	isOutbound       bool
	isOutboundMtx    sync.Mutex
	isForceClose     bool
	isForceCloseMtx  sync.Mutex

	RWStream       *bufio.ReadWriter
	VerValid       bool
	isConnected    bool
	isConnectedMtx sync.Mutex

	Config Config

	ListenerPeer *Peer

	HandleConnected    func(peerConn *PeerConn)
	HandleDisconnected func(peerConn *PeerConn)
	HandleFailed       func(peerConn *PeerConn)

	isUnitTest bool // default = false, use for unit test
}

// Start GET/SET func
func (peerConn *PeerConn) GetIsOutbound() bool {
	peerConn.isOutboundMtx.Lock()
	defer peerConn.isOutboundMtx.Unlock()
	return peerConn.isOutbound
}

func (peerConn *PeerConn) SetIsOutbound(isOutbound bool) {
	peerConn.isOutboundMtx.Lock()
	defer peerConn.isOutboundMtx.Unlock()
	peerConn.isOutbound = isOutbound
}

func (peerConn *PeerConn) GetIsForceClose() bool {
	peerConn.isForceCloseMtx.Lock()
	defer peerConn.isForceCloseMtx.Unlock()
	return peerConn.isForceClose
}

func (peerConn *PeerConn) SetIsForceClose(v bool) {
	peerConn.isForceCloseMtx.Lock()
	defer peerConn.isForceCloseMtx.Unlock()
	peerConn.isForceClose = v
}

func (peerConn *PeerConn) GetIsConnected() bool {
	peerConn.isConnectedMtx.Lock()
	defer peerConn.isConnectedMtx.Unlock()
	return peerConn.isConnected
}

func (peerConn *PeerConn) SetIsConnected(v bool) {
	peerConn.isConnectedMtx.Lock()
	defer peerConn.isConnectedMtx.Unlock()
	peerConn.isConnected = v
}

func (p *PeerConn) VerAckReceived() bool {
	return p.verAckReceived
}

// updateState updates the state of the connection request.
func (p *PeerConn) SetConnState(connState ConnState) {
	p.stateMtx.Lock()
	defer p.stateMtx.Unlock()
	p.connState = connState
}

// State is the connection state of the requested connection.
func (p *PeerConn) GetConnState() ConnState {
	p.stateMtx.RLock()
	defer p.stateMtx.RUnlock()
	connState := p.connState
	return connState
}

// end GET/SET func

// readString - read data from received message on stream
// and convert to string format
func (peerConn *PeerConn) readString(rw *bufio.ReadWriter, delim byte, maxReadBytes int) (string, error) {
	buf := make([]byte, 0)
	bufL := 0
	// Loop to read byte to byte
	for {
		b, err := rw.ReadByte()
		if err != nil {
			return "", err
		}
		// break byte buf after get a delim
		if b == delim {
			break
		}
		// continue add read byte to buf if not find a delim
		buf = append(buf, b)
		bufL++
		if bufL > maxReadBytes {
			return "", errors.New("limit bytes for message")
		}
	}

	// convert byte buf to string format
	return string(buf), nil
}

// processInMessageString - this is sub-function of InMessageHandler
// after receiving a good message from stream,
// we need analyze it and process with corresponding message type
func (peerConn *PeerConn) processInMessageString(msgStr string) error {
	// Parse Message header from last 24 bytes header message
	jsonDecodeBytesRaw, _ := hex.DecodeString(msgStr)

	// cache message hash
	hashMsgRaw := common.HashH(jsonDecodeBytesRaw).String()
	if peerConn.ListenerPeer != nil {
		if err := peerConn.ListenerPeer.HashToPool(hashMsgRaw); err != nil {
			Logger.log.Error(err)
			return err
		}
	}
	// unzip data before process
	jsonDecodeBytes, err := common.GZipToBytes(jsonDecodeBytesRaw)
	if err != nil {
		Logger.log.Error("Can not unzip from message")
		Logger.log.Error(err)
		return err
	}

	Logger.log.Infof("In message content : %s", string(jsonDecodeBytes))

	// Parse Message body
	messageBody := jsonDecodeBytes[:len(jsonDecodeBytes)-wire.MessageHeaderSize]

	messageHeader := jsonDecodeBytes[len(jsonDecodeBytes)-wire.MessageHeaderSize:]

	// get cmd type in header message
	commandInHeader := bytes.Trim(messageHeader[:wire.MessageCmdTypeSize], "\x00")
	commandType := string(messageHeader[:len(commandInHeader)])
	// convert to particular message from message cmd type
	message, err := wire.MakeEmptyMessage(string(commandType))
	if err != nil {
		Logger.log.Error("Can not find particular message for message cmd type")
		Logger.log.Error(err)
		return err
	}

	if len(jsonDecodeBytes) > message.MaxPayloadLength(1) {
		Logger.log.Errorf("Msg size exceed MsgType %s max size, size %+v | max allow is %+v \n", commandType, len(jsonDecodeBytes), message.MaxPayloadLength(1))
		return err
	}
	// check forward
	if peerConn.Config.MessageListeners.GetCurrentRoleShard != nil {
		cRole, cShard := peerConn.Config.MessageListeners.GetCurrentRoleShard()
		if cShard != nil {
			fT := messageHeader[wire.MessageCmdTypeSize]
			if fT == MessageToShard {
				fS := messageHeader[wire.MessageCmdTypeSize+1]
				if *cShard != fS {
					if peerConn.Config.MessageListeners.PushRawBytesToShard != nil {
						peerConn.Config.MessageListeners.PushRawBytesToShard(peerConn, &jsonDecodeBytesRaw, *cShard)
					}
					return err
				}
			}
		}
		if cRole != "" {
			fT := messageHeader[wire.MessageCmdTypeSize]
			if fT == MessageToBeacon && cRole != "beacon" {
				if peerConn.Config.MessageListeners.PushRawBytesToBeacon != nil {
					peerConn.Config.MessageListeners.PushRawBytesToBeacon(peerConn, &jsonDecodeBytesRaw)
				}
				return err
			}
		}
	}

	err = json.Unmarshal(messageBody, &message)
	if err != nil {
		Logger.log.Error("Can not parse struct from json message")
		Logger.log.Error(err)
		return err
	}
	realType := reflect.TypeOf(message)
	Logger.log.Infof("Cmd message type of struct %s", realType.String())

	// cache message hash
	if peerConn.ListenerPeer != nil {
		hashMsg := message.Hash()
		if err := peerConn.ListenerPeer.HashToPool(hashMsg); err != nil {
			Logger.log.Error(err)
			return err
		}
	}

	// process message for each of message type
	switch realType {
	case reflect.TypeOf(&wire.MessageTx{}):
		if peerConn.Config.MessageListeners.OnTx != nil {
			peerConn.Config.MessageListeners.OnTx(peerConn, message.(*wire.MessageTx))
		}
	case reflect.TypeOf(&wire.MessageTxToken{}):
		if peerConn.Config.MessageListeners.OnTxToken != nil {
			peerConn.Config.MessageListeners.OnTxToken(peerConn, message.(*wire.MessageTxToken))
		}
	case reflect.TypeOf(&wire.MessageTxPrivacyToken{}):
		if peerConn.Config.MessageListeners.OnTxPrivacyToken != nil {
			peerConn.Config.MessageListeners.OnTxPrivacyToken(peerConn, message.(*wire.MessageTxPrivacyToken))
		}
	case reflect.TypeOf(&wire.MessageBlockShard{}):
		if peerConn.Config.MessageListeners.OnBlockShard != nil {
			peerConn.Config.MessageListeners.OnBlockShard(peerConn, message.(*wire.MessageBlockShard))
		}
	case reflect.TypeOf(&wire.MessageBlockBeacon{}):
		if peerConn.Config.MessageListeners.OnBlockBeacon != nil {
			peerConn.Config.MessageListeners.OnBlockBeacon(peerConn, message.(*wire.MessageBlockBeacon))
		}
	case reflect.TypeOf(&wire.MessageCrossShard{}):
		if peerConn.Config.MessageListeners.OnCrossShard != nil {
			peerConn.Config.MessageListeners.OnCrossShard(peerConn, message.(*wire.MessageCrossShard))
		}
	case reflect.TypeOf(&wire.MessageShardToBeacon{}):
		if peerConn.Config.MessageListeners.OnShardToBeacon != nil {
			peerConn.Config.MessageListeners.OnShardToBeacon(peerConn, message.(*wire.MessageShardToBeacon))
		}
	case reflect.TypeOf(&wire.MessageGetBlockBeacon{}):
		if peerConn.Config.MessageListeners.OnGetBlockBeacon != nil {
			peerConn.Config.MessageListeners.OnGetBlockBeacon(peerConn, message.(*wire.MessageGetBlockBeacon))
		}
	case reflect.TypeOf(&wire.MessageGetBlockShard{}):
		if peerConn.Config.MessageListeners.OnGetBlockShard != nil {
			peerConn.Config.MessageListeners.OnGetBlockShard(peerConn, message.(*wire.MessageGetBlockShard))
		}
	case reflect.TypeOf(&wire.MessageGetCrossShard{}):
		if peerConn.Config.MessageListeners.OnGetCrossShard != nil {
			peerConn.Config.MessageListeners.OnGetCrossShard(peerConn, message.(*wire.MessageGetCrossShard))
		}
	case reflect.TypeOf(&wire.MessageGetShardToBeacon{}):
		if peerConn.Config.MessageListeners.OnGetShardToBeacon != nil {
			peerConn.Config.MessageListeners.OnGetShardToBeacon(peerConn, message.(*wire.MessageGetShardToBeacon))
		}
	case reflect.TypeOf(&wire.MessageVersion{}):
		if peerConn.Config.MessageListeners.OnVersion != nil {
			versionMessage := message.(*wire.MessageVersion)
			peerConn.Config.MessageListeners.OnVersion(peerConn, versionMessage)
		}
	case reflect.TypeOf(&wire.MessageVerAck{}):
		peerConn.verAckReceived = true
		if peerConn.Config.MessageListeners.OnVerAck != nil {
			peerConn.Config.MessageListeners.OnVerAck(peerConn, message.(*wire.MessageVerAck))
		}
	case reflect.TypeOf(&wire.MessageGetAddr{}):
		if peerConn.Config.MessageListeners.OnGetAddr != nil {
			peerConn.Config.MessageListeners.OnGetAddr(peerConn, message.(*wire.MessageGetAddr))
		}
	case reflect.TypeOf(&wire.MessageAddr{}):
		if peerConn.Config.MessageListeners.OnGetAddr != nil {
			peerConn.Config.MessageListeners.OnAddr(peerConn, message.(*wire.MessageAddr))
		}
	case reflect.TypeOf(&wire.MessageBFTPropose{}):
		if peerConn.Config.MessageListeners.OnBFTMsg != nil {
			peerConn.Config.MessageListeners.OnBFTMsg(peerConn, message.(*wire.MessageBFTPropose))
		}
	case reflect.TypeOf(&wire.MessageBFTProposeV2{}):
		if peerConn.Config.MessageListeners.OnBFTMsg != nil {
			peerConn.Config.MessageListeners.OnBFTMsg(peerConn, message.(*wire.MessageBFTProposeV2))
		}
	case reflect.TypeOf(&wire.MessageBFTPrepareV2{}):
		if peerConn.Config.MessageListeners.OnBFTMsg != nil {
			peerConn.Config.MessageListeners.OnBFTMsg(peerConn, message.(*wire.MessageBFTPrepareV2))
		}
	case reflect.TypeOf(&wire.MessageBFTAgree{}):
		if peerConn.Config.MessageListeners.OnBFTMsg != nil {
			peerConn.Config.MessageListeners.OnBFTMsg(peerConn, message.(*wire.MessageBFTAgree))
		}
	case reflect.TypeOf(&wire.MessageBFTCommit{}):
		if peerConn.Config.MessageListeners.OnBFTMsg != nil {
			peerConn.Config.MessageListeners.OnBFTMsg(peerConn, message.(*wire.MessageBFTCommit))
		}
	case reflect.TypeOf(&wire.MessageBFTReady{}):
		if peerConn.Config.MessageListeners.OnBFTMsg != nil {
			peerConn.Config.MessageListeners.OnBFTMsg(peerConn, message.(*wire.MessageBFTReady))
		}
	case reflect.TypeOf(&wire.MessageBFTReq{}):
		if peerConn.Config.MessageListeners.OnBFTMsg != nil {
			peerConn.Config.MessageListeners.OnBFTMsg(peerConn, message.(*wire.MessageBFTReq))
		}
	case reflect.TypeOf(&wire.MessagePeerState{}):
		if peerConn.Config.MessageListeners.OnPeerState != nil {
			peerConn.Config.MessageListeners.OnPeerState(peerConn, message.(*wire.MessagePeerState))
		}
	case reflect.TypeOf(&wire.MessageMsgCheck{}):
		peerConn.handleMsgCheck(message.(*wire.MessageMsgCheck))
	case reflect.TypeOf(&wire.MessageMsgCheckResp{}):
		peerConn.handleMsgCheckResp(message.(*wire.MessageMsgCheckResp))
	default:
		Logger.log.Warnf("InMessageHandler Received unhandled message of type % from %v", realType, peerConn)
	}
	// MONITOR INBOUND MESSAGE
	StoreInboundPeerMessage(message, time.Now().Unix())
	return nil
}

// InMessageHandler - Handle all in-coming message
// We receive a message with stream connection  of peer-to-peer
// convert to string data
// check type object which map with string data
// call corresponding function to process message
func (peerConn *PeerConn) InMessageHandler(rw *bufio.ReadWriter) error {
	peerConn.SetIsConnected(true)
	for {
		Logger.log.Infof("PEER %s (address: %s) Reading stream", peerConn.RemotePeer.PeerID.Pretty(), peerConn.RemotePeer.RawAddress)

		str, errR := peerConn.readString(rw, DelimMessageByte, SpamMessageSize)
		if errR != nil {
			// we has an error when read stream message an can not parse to string data
			peerConn.SetIsConnected(false)
			Logger.log.Error("---------------------------------------------------------------------")
			Logger.log.Errorf("InMessageHandler ERROR %s %s", peerConn.RemotePeerID.Pretty(), peerConn.RemotePeer.RawAddress)
			Logger.log.Error(errR)
			Logger.log.Errorf("InMessageHandler QUIT")
			Logger.log.Error("---------------------------------------------------------------------")
			close(peerConn.cWrite)
			return errR
		}

		if str != DelimMessageStr {
			// Get an good message, make an process to do something on it
			if !peerConn.isUnitTest {
				// not use for unit test -> call go routine for process
				go peerConn.processInMessageString(str)
			} else {
				// not use for unit test -> not call go routine for process
				// and break for loop
				peerConn.processInMessageString(str)
				return nil
			}
		}
	}
}

// OutMessageHandler handles the queuing of outgoing data for the peer. This runs as
// a muxer for various sources of input so we can ensure that server and peer
// handlers will not block on us sending a message.  That data is then passed on
// to outHandler to be actually written.
func (peerConn *PeerConn) OutMessageHandler(rw *bufio.ReadWriter) {
	for {
		select {
		case outMsg := <-peerConn.sendMessageQueue:
			{
				var sendString string
				if outMsg.rawBytes != nil && len(*outMsg.rawBytes) > 0 {
					Logger.log.Infof("OutMessageHandler with raw bytes")
					message := hex.EncodeToString(*outMsg.rawBytes)
					message += DelimMessageStr
					sendString = message
					Logger.log.Infof("Send a messageHex raw bytes to %s", peerConn.RemotePeer.PeerID.Pretty())
				} else {
					// Create and send messageHex
					messageBytes, err := outMsg.message.JsonSerialize()
					if err != nil {
						Logger.log.Error("Can not serialize json format for messageHex:" + outMsg.message.MessageType())
						Logger.log.Error(err)
						continue
					}

					// add 24 bytes headerBytes into messageHex
					headerBytes := make([]byte, wire.MessageHeaderSize)
					cmdType, messageErr := wire.GetCmdType(reflect.TypeOf(outMsg.message))
					if messageErr != nil {
						Logger.log.Error("Can not get cmd type for " + outMsg.message.MessageType())
						Logger.log.Error(messageErr)
						continue
					}
					copy(headerBytes[:], []byte(cmdType))
					copy(headerBytes[wire.MessageCmdTypeSize:], []byte{outMsg.forwardType})
					if outMsg.forwardValue != nil {
						copy(headerBytes[wire.MessageCmdTypeSize+1:], []byte{*outMsg.forwardValue})
					}
					messageBytes = append(messageBytes, headerBytes...)
					Logger.log.Infof("OutMessageHandler TYPE %s CONTENT %s", cmdType, string(messageBytes))

					// zip data before send
					messageBytes, err = common.GZipFromBytes(messageBytes)
					if err != nil {
						Logger.log.Error("Can not gzip for messageHex:" + outMsg.message.MessageType())
						Logger.log.Error(err)
						continue
					}
					messageHex := hex.EncodeToString(messageBytes)
					//Logger.log.Infof("Content in hex encode: %s", string(messageHex))
					// add end character to messageHex (delim '\n')
					messageHex += DelimMessageStr

					// send on p2p stream
					Logger.log.Infof("Send a messageHex %s to %s", outMsg.message.MessageType(), peerConn.RemotePeer.PeerID.Pretty())
					sendString = messageHex
				}
				// MONITOR OUTBOUND MESSAGE
				if outMsg.message != nil {
					StoreOutboundPeerMessage(outMsg.message, time.Now().Unix())
				}

				_, err := rw.Writer.WriteString(sendString)
				if err != nil {
					Logger.log.Critical("OutMessageHandler WriteString error", err)
					continue
				}
				err = rw.Writer.Flush()
				if err != nil {
					Logger.log.Critical("OutMessageHandler Flush error", err)
					continue
				}
				continue
			}
		case <-peerConn.cWrite:
			Logger.log.Infof("OutMessageHandler QUIT %s %s", peerConn.RemotePeerID.Pretty(), peerConn.RemotePeer.RawAddress)

			peerConn.SetIsConnected(false)

			close(peerConn.cDisconnect)

			if peerConn.HandleDisconnected != nil {
				go peerConn.HandleDisconnected(peerConn)
			}

			return
		}
	}
}

// checkMessageHashBeforeSend - pre-process message before pushing it into Send Queue
func (peerConn *PeerConn) checkMessageHashBeforeSend(hash string) bool {
	numRetries := 0
BeginCheckHashMessage:
	numRetries++
	bTimeOut := false
	// new model for received response
	peerConn.cMsgHash[hash] = make(chan bool)
	cTimeOut := make(chan struct{})
	bCheck := false
	// send msg for check has
	go func() {
		msgCheck, err := wire.MakeEmptyMessage(wire.CmdMsgCheck)
		if err != nil {
			Logger.log.Error("checkMessageHashBeforeSend error", err)
			return
		}
		msgCheck.(*wire.MessageMsgCheck).HashStr = hash
		peerConn.QueueMessageWithEncoding(msgCheck, nil, MessageToPeer, nil)
	}()
	// set time out for check message
	go func() {
		_, ok := <-time.NewTimer(MaxTimeoutCheckHashMessage * time.Second).C
		if !ok {
			if cTimeOut != nil {
				Logger.log.Infof("checkMessageHashBeforeSend TIMER time out %s", hash)
				bTimeOut = true
				close(cTimeOut)
			}
			return
		}
	}()
	Logger.log.Infof("checkMessageHashBeforeSend WAIT result check hash %s", hash)
	select {
	case bCheck = <-peerConn.cMsgHash[hash]:
		Logger.log.Infof("checkMessageHashBeforeSend RECEIVED hash %s bAccept %s", hash, bCheck)
		cTimeOut = nil
		break
	case <-cTimeOut:
		Logger.log.Infof("checkMessageHashBeforeSend RECEIVED time out %d", numRetries)
		cTimeOut = nil
		bTimeOut = true
		break
	}
	if cTimeOut == nil {
		delete(peerConn.cMsgHash, hash)
	}
	Logger.log.Infof("checkMessageHashBeforeSend FINISHED check hash %s %s", hash, bCheck)
	if bTimeOut && numRetries < MaxRetriesCheckHashMessage {
		goto BeginCheckHashMessage
	}
	return bCheck
}

// QueueMessageWithEncoding adds the passed Incognito message to the peer send
// queue. This function is identical to QueueMessage, however it allows the
// caller to specify the wire encoding type that should be used when
// encoding/decoding blocks and transactions.
//
// This function is safe for concurrent access.
func (peerConn *PeerConn) QueueMessageWithEncoding(msg wire.Message, doneChan chan<- struct{}, forwardType byte, forwardValue *byte) {
	Logger.log.Infof("QueueMessageWithEncoding %s %s", peerConn.RemotePeer.PeerID.Pretty(), msg.MessageType())
	go func() {
		if peerConn.GetIsConnected() {
			data, _ := msg.JsonSerialize()
			if len(data) >= HeavyMessageSize && msg.MessageType() != wire.CmdMsgCheck && msg.MessageType() != wire.CmdMsgCheckResp {
				hash := msg.Hash()
				Logger.log.Infof("QueueMessageWithEncoding HeavyMessageSize %s %s", hash, msg.MessageType())

				if peerConn.checkMessageHashBeforeSend(hash) {
					peerConn.sendMessageQueue <- outMsg{
						message:      msg,
						doneChan:     doneChan,
						forwardType:  forwardType,
						forwardValue: forwardValue,
					}
				}
			} else {
				peerConn.sendMessageQueue <- outMsg{
					message:      msg,
					doneChan:     doneChan,
					forwardType:  forwardType,
					forwardValue: forwardValue,
				}
			}
		}
	}()
}

func (peerConn *PeerConn) QueueMessageWithBytes(msgBytes *[]byte, doneChan chan<- struct{}) {
	Logger.log.Infof("QueueMessageWithBytes %s", peerConn.RemotePeer.PeerID.Pretty())
	if msgBytes == nil || len(*msgBytes) <= 0 {
		return
	}
	go func() {
		if peerConn.GetIsConnected() {
			if len(*msgBytes) >= HeavyMessageSize+wire.MessageHeaderSize {
				hash := common.HashH(*msgBytes).String()
				Logger.log.Infof("QueueMessageWithBytes HeavyMessageSize %s", hash)

				if peerConn.checkMessageHashBeforeSend(hash) {
					peerConn.sendMessageQueue <- outMsg{
						rawBytes: msgBytes,
						doneChan: doneChan,
					}
				}
			} else {
				peerConn.sendMessageQueue <- outMsg{
					rawBytes: msgBytes,
					doneChan: doneChan,
				}
			}
		}
	}()
}

func (p *PeerConn) handleMsgCheck(msg *wire.MessageMsgCheck) error {
	Logger.log.Infof("handleMsgCheck %s", msg.HashStr)
	msgResp, err := wire.MakeEmptyMessage(wire.CmdMsgCheckResp)
	if err != nil {
		Logger.log.Error("handleMsgCheck error", err)
		return err
	}
	if p.ListenerPeer.CheckHashPool(msg.HashStr) {
		msgResp.(*wire.MessageMsgCheckResp).HashStr = msg.HashStr
		msgResp.(*wire.MessageMsgCheckResp).Accept = false
	} else {
		msgResp.(*wire.MessageMsgCheckResp).HashStr = msg.HashStr
		msgResp.(*wire.MessageMsgCheckResp).Accept = true
	}
	p.QueueMessageWithEncoding(msgResp, nil, MessageToPeer, nil)
	return nil
}

func (p *PeerConn) handleMsgCheckResp(msg *wire.MessageMsgCheckResp) error {
	Logger.log.Infof("handleMsgCheckResp %s", msg.HashStr)
	m, ok := p.cMsgHash[msg.HashStr]
	if ok {
		if !p.isUnitTest {
			// if not unit test -> send channel to process
			m <- msg.Accept
		}
		return nil
	} else {
		return errors.New("not ok")
	}
}

// Close - close peer connection by close channel
func (p *PeerConn) Close() {
	if _, ok := <-p.cClose; ok {
		close(p.cClose)
	}
}

// ForceClose - set flag and close channel
func (p *PeerConn) ForceClose() {
	p.SetIsForceClose(true)
	close(p.cClose)
}
