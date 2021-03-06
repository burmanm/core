package routing

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

/*
  This is the InMemory store which will store only the latest available data.
  To store historical data also, change datastore to []Data and adjust some queries
*/
type Tracking struct {
	requestId string
	reply     chan Reply
	subs      []Key
}

type Key struct {
	Node, Measurement string
}

type InMemoryStore struct {
	mu        sync.RWMutex
	datastore map[string]map[string]Data

	trackMu  sync.Mutex
	tracking map[string]*Tracking

	subsMu       sync.RWMutex
	subscription map[Key][]*Tracking

	nextId int64
}

// Methods required by the Datastore interface
func NewInMemoryStore() *InMemoryStore {
	m := new(InMemoryStore)
	m.datastore = make(map[string]map[string]Data)
	m.tracking = make(map[string]*Tracking)
	m.subscription = make(map[Key][]*Tracking)
	return m
}

/*
  Equals subscription request..
*/
func (m *InMemoryStore) Subscribe(r *Request) (error, Reply) {
	rId := m.requestId()
	m.mu.RLock()
	defer m.mu.RUnlock()

	reply := Reply{RequestId: rId}

	if _, found := m.datastore[r.Node]; found {
		// Create requestId for this subscription request
		t := &Tracking{requestId: rId, reply: r.ReplyChan, subs: make([]Key, 0, 10)}

		// Add subscriptions for each key
		m.subsMu.Lock()
		for _, me := range r.Measurements {
			k := Key{Node: r.Node, Measurement: me}
			t.subs = append(t.subs, k)
			m.subscription[k] = append(m.subscription[k], t)
		}
		m.subsMu.Unlock()

		// Add for tracking purposes
		m.trackMu.Lock()
		m.tracking[rId] = t
		m.trackMu.Unlock()
	} else {
		return errors.New("Could not fetch requested data, node does not exists"), reply
	}
	return nil, reply
}

func (m *InMemoryStore) ReadImmediate(r *Request) (error, Reply) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data := make([]Data, 0, 10)

	if node, found := m.datastore[r.Node]; found {
		for _, me := range r.Measurements {
			if v, f := node[me]; f {
				data = append(data, v)
			}
		}
		reply := Reply{Node: r.Node, Datapoints: data}
		r.ReplyChan <- reply
		return nil, Reply{}
	} else {
		return errors.New("Could not find requested information"), Reply{}
	}
}

func (m *InMemoryStore) Write(w *Write) (error, Reply) {
	datapoints := make(map[string]struct{})
	for _, d := range w.Datapoints {
		datapoints[d.Measurement] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check that we have node registered..
	node, found := m.datastore[w.Node]
	if !found {
		m.datastore[w.Node] = make(map[string]Data)
		node = m.datastore[w.Node]
	}

	for _, datas := range node {
		if _, ok := datapoints[datas.Measurement]; !ok {
			delete(m.subscription, Key{Node: w.Node, Measurement: datas.Measurement})
			delete(m.datastore[w.Node], datas.Measurement)
		}
	}

	for _, data := range w.Datapoints {
		if data.Timestamp < 1 {
			// Add current timestamp if none was given
			data.Timestamp = time.Now().Unix()
		}
		node[data.Measurement] = data
		trackKey := Key{Node: w.Node, Measurement: data.Measurement}
		m.publish(trackKey, data.Value, data.Timestamp)
	}

	return nil, Reply{RequestId: m.requestId()}
}

func (m *InMemoryStore) Cancel(requestId string) error {
	m.trackMu.Lock()
	defer func() {
		m.trackMu.Unlock()
		m.subsMu.Unlock()
	}()
	m.subsMu.Lock()

	if _, found := m.tracking[requestId]; found {
		/*
			for _, k := range t.subs {
				for i, tt := range m.subscription[k] {
					if tt.requestId == requestId {
						d := append(tt[:i], tt[i+1:])
						m.subscription[k] = d
					}
				}
			}
		*/
		delete(m.tracking, requestId)
	} else {
		return errors.New("No subscription found for " + requestId)
	}

	return nil
}

func (m *InMemoryStore) requestId() string {
	newId := atomic.AddInt64(&m.nextId, 1)
	return fmt.Sprintf("REQ%07d", newId)
}

func (m *InMemoryStore) publish(key Key, value string, timestamp int64) error {
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()

	if ts, found := m.subscription[key]; found {
		for _, t := range ts {
			d := Data{Measurement: key.Measurement, Value: value, Timestamp: timestamp}
			data := []Data{d}
			r := Reply{RequestId: t.requestId, Node: key.Node, Datapoints: data}
			t.reply <- r
		}
	}
	return nil
}

func keymap(dataslice []Data) map[string]struct{} {
	var datapoints map[string]struct{} = make(map[string]struct{})
	for _, d := range dataslice {
		datapoints[d.Measurement] = struct{}{}
	}
	return datapoints
}

// Functions to support querying what datasources are available

func (m *InMemoryStore) NodeList() []string {
	keystore := m.datastore
	keys := make([]string, 0, len(keystore))
	for k := range keystore {
		keys = append(keys, k)
	}
	return keys
}

func (m *InMemoryStore) SourceList(node string) (error, []string) {
	keystore, found := m.datastore[node]
	if !found {
		return errors.New("Could not find node " + node), nil
	}
	keys := make([]string, 0, len(keystore))
	for k := range keystore {
		keys = append(keys, k)
	}
	return nil, keys
}
