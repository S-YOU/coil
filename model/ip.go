package model

import (
	"context"
	"net"
	"strconv"

	"github.com/coreos/etcd/clientv3"
	"github.com/cybozu-go/netutil"
)

func (m etcdModel) GetAllocatedIPs(ctx context.Context, block *net.IPNet) (map[string]net.IP, error) {
	prefix := ipKeyPrefix(block)
	resp, err := m.etcd.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	ips := make(map[string]net.IP)
	for _, kv := range resp.Kvs {
		offsetStr := string(kv.Key[len(prefix):])
		offset, err := strconv.Atoi(offsetStr)
		if err != nil {
			return nil, err
		}
		ip := netutil.IntToIP4(netutil.IP4ToInt(block.IP) + uint32(offset))
		ips[string(kv.Value)] = ip
	}
	return ips, nil
}

func (m etcdModel) AllocateIP(ctx context.Context, block *net.IPNet, key string) (net.IP, error) {
	resp, err := m.etcd.Get(ctx, ipKeyPrefix(block), clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		return nil, err
	}
	allocated := make(map[string]bool)
	for _, kv := range resp.Kvs {
		allocated[string(kv.Key)] = true
	}
	ones, bits := block.Mask.Size()
	blockSize := int(1 << uint(bits-ones))

	offset := -1
	for i := 0; i < blockSize; i++ {
		k := ipKey(block, i)
		if allocated[k] {
			continue
		}
		offset = i
		_, err = m.etcd.Put(ctx, k, key)
		if err != nil {
			return nil, err
		}
		break
	}
	if offset == -1 {
		return nil, ErrBlockIsFull
	}
	return netutil.IntToIP4(netutil.IP4ToInt(block.IP) + uint32(offset)), nil
}

func (m etcdModel) FreeIP(ctx context.Context, block *net.IPNet, ip net.IP) error {
	offset := netutil.IP4ToInt(ip) - netutil.IP4ToInt(block.IP)
	_, err := m.etcd.Delete(ctx, ipKey(block, int(offset)))
	return err
}
