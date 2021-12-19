package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/sirupsen/logrus"
)

// command of message
const (
	CommandPing = 100
	CommandPong = 101
)

// Server is a websocket implement of the Server
type Server struct {
	once    sync.Once
	id      string
	address string
	sync.Mutex
	// 会话列表
	users map[string]net.Conn
}

// NewServer NewServer
func NewServer(id, address string) *Server {
	return &Server{
		id:      id,
		address: address,
		users:   make(map[string]net.Conn, 100),
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	log := logrus.WithFields(logrus.Fields{"module": "Server", "listen": s.address, "id": s.id})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		//     upgrade  readLoop  close
		// 初始态 => 就绪态 => 运行态 => 死亡态
		//            |                 ↑
		//            +-----------------+ 认证失败
		//
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			_ = conn.Close()
			return
		}
		// 读取 userId
		user := r.URL.Query().Get("user")
		if user == "" {
			_ = conn.Close()
			return
		}

		// 添加用户到会话管理中
		old, ok := s.addUser(user, conn)
		if ok {
			// 断开旧的连接
			old.Close()
		}
		log.Infof("user %s in", user)

		// 启用 goroutine 读取客户端数据
		go func(user string, conn net.Conn) {
			err := s.readLoop(user, conn)
			if err != nil {
				log.Warn("readLoop - ", err)
			}
			_ = conn.Close()

			// 删除用户
			s.delUser(user)

			log.Infof("connection of %s closed", user)
		}(user, conn)
	})
	return http.ListenAndServe(s.address, mux)
}

func (s *Server) addUser(user string, conn net.Conn) (net.Conn, bool) {
	s.Lock()
	defer s.Unlock()
	old, ok := s.users[user] //返回旧的连接
	s.users[user] = conn     //缓存
	return old, ok
}

func (s *Server) delUser(user string) {
	s.Lock()
	defer s.Unlock()
	delete(s.users, user)
}

func (s *Server) readLoop(user string, conn net.Conn) error {
	for {
		// 客户端在指定时间内发送一条消息过来，可以是 ping 或正常数据包。
		_ = conn.SetReadDeadline(time.Now().Add(time.Minute * 2))

		// 从 TCP 缓冲区读取一帧消息。
		frame, err := ws.ReadFrame(conn)
		if err != nil {
			return err
		}
		if frame.Header.OpCode == ws.OpPing {
			// 返回一个 pong 消息
			_ = wsutil.WriteServerMessage(conn, ws.OpPong, nil)
			logrus.Info("write a pong...")
			continue
		}
		if frame.Header.OpCode == ws.OpClose {
			return errors.New("remote side close the conn")
		}
		logrus.Info(frame.Header)

		// 使用 Mask 解码数据包：
		// WebSocket 协议规定客户端发送数据时必须使用随机的 mask 值对消息体做编码，在服务端解码。
		if frame.Header.Masked {
			ws.Cipher(frame.Payload, frame.Header.Mask, 0)
		}
		// 接收文本帧内容：二进制或广播消息。
		if frame.Header.OpCode == ws.OpText {
			go s.handleText(user, string(frame.Payload))
		} else if frame.Header.OpCode == ws.OpBinary {
			go s.handleBinary(user, frame.Payload)
		}
	}
}

func (s *Server) handleText(user string, message string) {
	logrus.Infof("recv message %s from %s", message, user)
	s.Lock()
	defer s.Unlock()
	broadcast := fmt.Sprintf("%s -- FROM %s", message, user)
	for u, conn := range s.users {
		if u == user { // 不发给自己
			continue
		}
		logrus.Infof("send to %s : %s", u, broadcast)

		// 创建文本帧数据
		f := ws.NewTextFrame([]byte(message))
		err := conn.SetWriteDeadline(time.Now().Add(time.Second * 10))
		if err != nil {
			logrus.Errorf("write to %s failed, error: %v", user, err)
		}
		err = ws.WriteFrame(conn, f)
		if err != nil {
			logrus.Errorf("write to %s failed, error: %v", user, err)
		}
	}
}

func (s *Server) handleBinary(user string, message []byte) {
	logrus.Infof("recv message %v from %s", message, user)
	s.Lock()
	defer s.Unlock()
	// handleText ping request
	i := 0
	command := binary.BigEndian.Uint16(message[i : i+2])
	i += 2
	payloadLen := binary.BigEndian.Uint32(message[i : i+4])
	logrus.Infof("command: %v payloadLen: %v", command, payloadLen)
	if command == CommandPing {
		u := s.users[user]
		// return pong
		err := wsutil.WriteServerBinary(u, []byte{0, CommandPong, 0, 0, 0, 0})
		if err != nil {
			logrus.Errorf("write to %s failed, error: %v", user, err)
		}
	}
}

func NewServerCmd(ctx context.Context, version string) *cobra.Command {
	var id, listen string
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start chat server",
		RunE: func(cmd *cobra.Command, args []string) error {
			server := NewServer(id, listen)
			defer server.once.Do(func() {
				server.Lock()
				defer server.Unlock()
				for _, conn := range server.users {
					conn.Close()
				}
			})
			return server.Start()
		},
	}
	cmd.PersistentFlags().StringVarP(&id, "serverId", "i", "demo", "server id")
	cmd.PersistentFlags().StringVarP(&listen, "listen", "l", ":8000", "listen address")
	return cmd
}
