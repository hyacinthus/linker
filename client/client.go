package client

import (
	"hash/crc32"
	"net"
	"time"

	"github.com/wpajqz/linker"
)

const MaxPayload = 2048

type Handler func(*Context)

type Client struct {
	timeout          time.Duration
	conn             net.Conn
	protocolPacket   linker.Packet
	handlerContainer map[uint32]Handler
	packet           chan linker.Packet
	cancelHeartbeat  chan bool
	closeClient      chan bool
	running          chan bool
}

func NewClient(network, address string) *Client {
	client := &Client{
		timeout:          30 * time.Second,
		packet:           make(chan linker.Packet, 1024),
		handlerContainer: make(map[uint32]Handler),
		cancelHeartbeat:  make(chan bool, 1),
		closeClient:      make(chan bool, 1),
		running:          make(chan bool, 1),
	}

	conn, err := net.Dial(network, address)
	if err != nil {
		panic(err.Error())
	}

	client.conn = conn

	go func(string, string, net.Conn) {
		for {
			err := client.handleConnection(client.conn)
			if err != nil {
				client.running <- false
			}

			select {
			case r := <-client.running:
				if !r {
					for {
						//服务端timeout设置影响链接延时时间
						conn, err := net.Dial(network, address)
						if err == nil {
							client.conn = conn
							break
						}
					}
				}
			case <-client.closeClient:
				return
			}
		}
	}(network, address, client.conn)

	return client
}

func (c *Client) Heartbeat(interval time.Duration, pb interface{}) error {
	data := []byte("heartbeat")
	op := crc32.ChecksumIEEE(data)

	p, err := c.protocolPacket.Pack(op, []byte(""), pb)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(interval * time.Second)
	for {
		select {
		case <-ticker.C:
			c.packet <- p
		case <-c.cancelHeartbeat:
			return nil
		}
	}
}

func (c *Client) SetProtocolPacket(packet linker.Packet) {
	c.protocolPacket = packet
}

func (c *Client) SetTimeout(timeout time.Duration) {
	c.timeout = timeout
}

func (c *Client) Close() {
	c.cancelHeartbeat <- true
	c.closeClient <- true
	close(c.cancelHeartbeat)
	close(c.closeClient)
}

// 向服务端发送请求，同步处理服务端返回结果
func (c *Client) SyncCall(operator string, pb interface{}, callback func(*Context)) error {
	data := []byte(operator)
	op := crc32.ChecksumIEEE(data)

	p, err := c.protocolPacket.Pack(op, []byte(`{"auth":"paul"}`), pb)
	if err != nil {
		return err
	}

	c.packet <- p

	// 对数据请求的返回状态进行处理,同步阻塞处理机制
	ch := make(chan bool)
	c.AddMessageListener(operator, func(ctx *Context) {
		callback(ctx)
		c.RemoveMessageListener(operator)
		ch <- true
	})

	<-ch

	return nil
}

// 向服务端发送请求，异步处理服务端返回结果
func (c *Client) AsyncCall(operator string, pb interface{}, callback func(*Context)) error {
	data := []byte(operator)
	op := crc32.ChecksumIEEE(data)

	p, err := c.protocolPacket.Pack(op, []byte("true"), pb)
	if err != nil {
		return err
	}
	c.packet <- p

	c.AddMessageListener(operator, func(ctx *Context) {
		callback(ctx)
		c.RemoveMessageListener(operator)
	})

	return nil
}

// 添加事件监听器
func (c *Client) AddMessageListener(listener string, callback func(*Context)) {
	operator := crc32.ChecksumIEEE([]byte(listener))
	c.handlerContainer[operator] = callback
}

// 移除事件监听器
func (c *Client) RemoveMessageListener(listener string) {
	operator := crc32.ChecksumIEEE([]byte(listener))
	delete(c.handlerContainer, operator)
}
