package worker

type Chunk struct {
	Index int // -1 is a pseudo chunk for 0 byte files.
	File  *File
}
