package utils

// ValIdx represents a value and its original index
type ValIdx struct {
	Val   float64
	Index int
}

// TopKFinder is a structure to efficiently find top K elements
// It uses a min-heap to maintain the K largest elements
type TopKFinder struct {
	minHeap []ValIdx // Min heap to store the largest K elements
	indices []int    // Result slice to store the indices
}

// NewTopKFinder creates a new TopKFinder with preallocated memory
// maxK: maximum value of K that will be used
func NewTopKFinder(maxK int) *TopKFinder {
	return &TopKFinder{
		minHeap: make([]ValIdx, 0, maxK),
		indices: make([]int, 0, maxK),
	}
}

// FindTopK finds the indices of top K largest elements in nums
// nums: the input float64 slice
// k: the number of largest elements to find
// Returns a slice of indices corresponding to the largest elements
func (f *TopKFinder) FindTopK(nums []float64, k int) []int {
	if k <= 0 || len(nums) == 0 {
		return []int{}
	}

	// Limit k to the length of nums
	if k > len(nums) {
		k = len(nums)
	}

	// Reset slices to reuse memory
	f.minHeap = f.minHeap[:0]
	f.indices = f.indices[:0]

	// Initialize min-heap with first k elements
	for i := 0; i < k; i++ {
		f.minHeap = append(f.minHeap, ValIdx{nums[i], i})
	}

	// Heapify the first k elements
	f.buildMinHeap(k)

	// Process remaining elements
	for i := k; i < len(nums); i++ {
		// If current element is larger than the smallest element in heap
		if nums[i] > f.minHeap[0].Val {
			// Replace the root with the new element
			f.minHeap[0] = ValIdx{nums[i], i}
			// Restore heap property
			f.siftDown(0, k-1)
		}
	}

	// Extract indices from the heap
	f.indices = make([]int, k)
	for i := 0; i < k; i++ {
		f.indices[i] = f.minHeap[i].Index
	}

	return f.indices
}

// FindTopKWithBuffer finds the indices of top K largest elements in nums
// using a provided buffer to store the results
// nums: the input float64 slice
// k: the number of largest elements to find
// result: a pre-allocated buffer to store the results
// Returns the result slice filled with indices
func (f *TopKFinder) FindTopKWithBuffer(nums []float64, k int, result []int) []int {
	if k <= 0 || len(nums) == 0 {
		return result[:0]
	}

	// Limit k to the length of nums and the size of result
	if k > len(nums) {
		k = len(nums)
	}
	if k > len(result) {
		k = len(result)
	}

	// Reset minHeap to reuse memory
	f.minHeap = f.minHeap[:0]

	// Initialize min-heap with first k elements
	for i := 0; i < k; i++ {
		f.minHeap = append(f.minHeap, ValIdx{nums[i], i})
	}

	// Heapify the first k elements
	f.buildMinHeap(k)

	// Process remaining elements
	for i := k; i < len(nums); i++ {
		// If current element is larger than the smallest element in heap
		if nums[i] > f.minHeap[0].Val {
			// Replace the root with the new element
			f.minHeap[0] = ValIdx{nums[i], i}
			// Restore heap property
			f.siftDown(0, k-1)
		}
	}

	// Fill the result buffer with indices
	for i := 0; i < k; i++ {
		result[i] = f.minHeap[i].Index
	}

	return result[:k]
}

// buildMinHeap builds a min-heap from an unsorted slice
// size: the size of the heap
func (f *TopKFinder) buildMinHeap(size int) {
	// Start from the last non-leaf node and sift down
	for i := size/2 - 1; i >= 0; i-- {
		f.siftDown(i, size-1)
	}
}

// siftDown moves an element down the heap until the heap property is restored
// root: the index of the element to sift down
// end: the last valid index in the heap
func (f *TopKFinder) siftDown(root, end int) {
	for {
		// Calculate the left child index
		child := root*2 + 1

		// If we're beyond the heap bounds, we're done
		if child > end {
			break
		}

		// Choose the smaller child (for min-heap)
		if child+1 <= end && f.minHeap[child].Val > f.minHeap[child+1].Val {
			child++
		}

		// If the heap property is satisfied, we're done
		if f.minHeap[root].Val <= f.minHeap[child].Val {
			break
		}

		// Swap the root with the smaller child
		f.minHeap[root], f.minHeap[child] = f.minHeap[child], f.minHeap[root]

		// Continue sifting down from the child position
		root = child
	}
}

// SortIndices sorts the indices by their corresponding values in descending order
// This can be called after FindTopK if you need the indices in sorted order
func (f *TopKFinder) SortIndices(nums []float64) {
	// Simple insertion sort since k is usually small
	for i := 1; i < len(f.indices); i++ {
		j := i
		for j > 0 && nums[f.indices[j]] > nums[f.indices[j-1]] {
			f.indices[j], f.indices[j-1] = f.indices[j-1], f.indices[j]
			j--
		}
	}
}
