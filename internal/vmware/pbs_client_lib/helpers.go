package pbsclientgo

import "math/bits"

func powOf2(e uint64) uint64 {
	return 1 << e
}

func logOf2(k uint64) uint64 {
	return 64 - uint64(bits.LeadingZeros64(k)) - 1
}

func makeBstInner(input []GoodByeItem, n, e uint64, output *[]GoodByeItem, i uint64) {
	if n == 0 {
		return
	}
	p := powOf2(e - 1)
	q := powOf2(e)
	var k uint64
	if n >= p-1+p/2 {
		k = (q - 2) / 2
	} else {
		v := p - 1 + p/2 - n
		k = (q-2)/2 - v
	}

	(*output)[i] = input[k]
	makeBstInner(input, k, e-1, output, i*2+1)
	makeBstInner(input[k+1:], n-k-1, e-1, output, i*2+2)
}

func caMakeBst(input []GoodByeItem, output *[]GoodByeItem) {
	n := uint64(len(input))
	makeBstInner(input, n, logOf2(n)+1, output, 0)
}
