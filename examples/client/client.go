package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type StartOptions struct {
	address string
	user    string
}

type handler struct {
	conn      net.Conn
	close     chan struct{}
	recv      chan []byte
	heartbeat time.Duration
}

// NewClientCmd NewClientCmd
func NewClientCmd(ctx context.Context) *cobra.Command {
	opts := &StartOptions{}
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Start client",
		RunE: func(cmd *cobra.Command, args []string) error {
			return func(ctx context.Context, opts *StartOptions) error {
				url := fmt.Sprintf("%s?user=%s", opts.address, opts.user)
				logrus.Info("connect to ", url)

				// 连接到服务、分别启动两个 goroutine 发送消息和心跳，并返回 handler 对象
				h, err := connect(url)
				if err != nil {
					return err
				}
				go func() {
					// 读取消息并显示
					for msg := range h.recv {
						logrus.Info("Receive message: ", string(msg))
					}
				}()

				tk := time.NewTicker(time.Second * 6)
				for {
					select {
					case <-tk.C:
						//每 6 秒发送一个消息
						msg := "hello"
						logrus.Info("send message: ", msg)
						// 发送消息时会设置 Masked=true，对数据包做一次编码，Mask 值随机生成。
						err := wsutil.WriteClientText(h.conn, []byte(msg))
						if err != nil {
							logrus.Error("sendText - ", err)
						}
					case <-h.close:
						logrus.Printf("connection closed")
						return nil
					}
				}
			}(ctx, opts)
		},
	}
	cmd.PersistentFlags().StringVarP(&opts.address, "address", "a", "ws://127.0.0.1:8000", "server address")
	cmd.PersistentFlags().StringVarP(&opts.user, "user", "u", "", "user")
	return cmd
}

func connect(addr string) (*handler, error) {
	_, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	conn, _, _, err := ws.Dial(context.Background(), addr)
	if err != nil {
		return nil, err
	}

	h := handler{
		conn:      conn,
		close:     make(chan struct{}, 1),
		recv:      make(chan []byte, 10),
		heartbeat: time.Second * 50,
	}

	// 发送消息。
	go func() {
		err := h.readLoop(conn)
		if err != nil {
			logrus.Warn("readLoop - ", err)
		}
		// 通知上层
		h.close <- struct{}{}
	}()

	// 发送心跳。
	go func() {
		err := h.heartbeatLoop()
		if err != nil {
			logrus.Info("heartbeatLoop - ", err)
		}
	}()

	return &h, nil
}

func (h *handler) readLoop(conn net.Conn) error {
	logrus.Info("readLoop started")
	// 要求在指定时间 heartbeat（50秒）*3 内，可以读到数据
	err := h.conn.SetReadDeadline(time.Now().Add(h.heartbeat * 3))
	if err != nil {
		return err
	}
	for {
		frame, err := ws.ReadFrame(conn)
		if err != nil {
			return err
		}
		if frame.Header.OpCode == ws.OpPong {
			// 重置读取超时时间
			logrus.Info("recv a pong...")
			_ = h.conn.SetReadDeadline(time.Now().Add(h.heartbeat * 3))
		}
		if frame.Header.OpCode == ws.OpClose {
			return errors.New("remote side close the channel")
		}
		if frame.Header.OpCode == ws.OpText {
			h.recv <- frame.Payload
		}
	}
}

func (h *handler) heartbeatLoop() error {
	logrus.Info("heartbeatLoop started")
	tick := time.NewTicker(h.heartbeat)
	for range tick.C {
		// 发送一个 ping 的心跳包给服务端
		logrus.Info("ping...")
		_ = h.conn.SetWriteDeadline(time.Now().Add(time.Second * 10))

		if err := wsutil.WriteClientMessage(h.conn, ws.OpPing, nil); err != nil {
			return err
		}
	}
	return nil
}
