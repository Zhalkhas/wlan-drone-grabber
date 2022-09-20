package main

import (
	"fmt"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
	"golang.org/x/exp/slices"
	"log"
	"os"
	"sort"
	"time"
)

type StreamPacket struct {
	ID           uint32
	ChunkIndex   byte
	UnknownValue byte
	PacketSize   int
	Payload      []byte `json:"-"`
	Time         time.Time
}

func (p StreamPacket) String() string {
	return fmt.Sprintf("ID: 0x%08x Num: %d Unknown:0x%02x PacketSize:%d bytes"+
		"\nPayload [0x%02x 0x%02x 0x%02x 0x%02x ... 0x%02x 0x%02x 0x%02x 0x%02x]",
		p.ID, p.ChunkIndex, p.UnknownValue, p.PacketSize,
		p.Payload[0], p.Payload[1], p.Payload[2], p.Payload[3],
		p.Payload[len(p.Payload)-4], p.Payload[len(p.Payload)-3],
		p.Payload[len(p.Payload)-2], p.Payload[len(p.Payload)-1])
}

type VideoFrame struct {
	JpegData []byte
	Time     time.Time
}

func main() {
	f, err := os.Open("./video.pcapng")
	if err != nil {
		log.Panicln(err)
	}

	reader, err := pcapgo.NewNgReader(f, pcapgo.NgReaderOptions{})
	if err != nil {
		log.Panicln(err)
	}

	packetSource := gopacket.NewPacketSource(reader, reader.LinkType())
	packetsDecoded := make(chan *StreamPacket)

	go func() {
		for packet := range packetSource.Packets() {
			udpPacket, isUdp := packet.TransportLayer().(*layers.UDP)
			if isUdp && udpPacket != nil {
				data := udpPacket.LayerPayload()
				if data[0] == 99 && data[1] == 99 && len(data) > 54 {
					id := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[12])<<8 | uint32(data[13])
					packet := &StreamPacket{
						ID:           id,
						ChunkIndex:   data[48],
						UnknownValue: data[50],
						PacketSize:   len(data),
						Payload:      data[54:],
						Time:         packet.Metadata().Timestamp,
					}
					packetsDecoded <- packet
				}
			}
		}
		close(packetsDecoded)
	}()

	res := make(map[uint32][]*StreamPacket)
	for packet := range packetsDecoded {
		_, found := res[packet.ID]
		if found {
			res[packet.ID] = append(res[packet.ID], packet)
		} else {
			res[packet.ID] = []*StreamPacket{packet}
		}
	}

	imagesChan := make(chan *VideoFrame)
	go func() {
		for _, imageChunks := range res {
			sort.Slice(imageChunks, func(i, j int) bool {
				return imageChunks[i].ChunkIndex < imageChunks[j].ChunkIndex
			})
			totalChunks := int(imageChunks[len(imageChunks)-1].ChunkIndex)
			firstChunk := imageChunks[0].Payload
			lastChunk := imageChunks[len(imageChunks)-1].Payload
			if lastChunk[len(lastChunk)-2] == 0xff && lastChunk[len(lastChunk)-1] == 0xd9 && firstChunk[0] == 0xff && firstChunk[1] == 0xd8 {
				fmt.Println("total chunks", totalChunks)
				imageData := make([]byte, 0)
				for i := 1; i < totalChunks; i++ {
					chunkIndex := slices.IndexFunc(imageChunks, func(chunk *StreamPacket) bool {
						return int(chunk.ChunkIndex) == i
					})
					if chunkIndex == -1 {
						emptyBuf := make([]byte, 1400)
						imageData = append(imageData, emptyBuf...)
					} else {
						imageData = append(imageData, imageChunks[chunkIndex].Payload...)
					}
				}
				fmt.Printf("head [0x%02x 0x%02x 0x%02x 0x%02x] "+
					"tail [0x%02x 0x%02x 0x%02x 0x%02x]\n",
					imageData[0], imageData[1], imageData[2], imageData[3],
					imageData[len(imageData)-4], imageData[len(imageData)-3], imageData[len(imageData)-2], imageData[len(imageData)-1],
				)
				imagesChan <- &VideoFrame{JpegData: imageData, Time: imageChunks[0].Time}
			}
		}
		close(imagesChan)
	}()

	imageFrames := make([]*VideoFrame, 0)
	for image := range imagesChan {
		imageFrames = append(imageFrames, image)
	}
	sort.Slice(imageFrames, func(i, j int) bool {
		return imageFrames[i].Time.Unix() < imageFrames[j].Time.Unix()
	})
	for i := 0; i < len(imageFrames); i++ {
		frame, err := os.Create(fmt.Sprintf("frame_%d.jpg", i))
		if err != nil {
			log.Panicln(err)
		}
		frame.Write(imageFrames[i].JpegData)
		frame.Close()
	}
}
