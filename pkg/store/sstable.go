package store

const Magic = "流萤"
const ReStartInterval = 16

type Record struct {
	Key string
	Val string
}
type SStable struct {
	rs []Record
	bf *BloomFilter
}

func NewSStable(rs []Record) (*SStable, error) {
	s := &SStable{}
	s.rs = rs
	bf, err := NewBloomFilterWithSize(32, 4)
	if err != nil {
		return nil, err
	}
	s.bf = bf
	for _, v := range rs {
		s.bf.Add([]byte(v.Key))
	}
	return s, nil
}

// [shared key] [key] [deta_len] [key delate] [data]
func DecodeRcWithTrie(rs []Record) []byte {
   return nil
}
func SharedLen(target []byte, source []byte) int {
	ml := min(len(target), len(source))
	for i := 0; i < ml; i++ {
		if target[ml] != source[ml] {
			 return i
		}
	}
	return ml
}
