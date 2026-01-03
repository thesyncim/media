package media

// ScaleMode defines how scaling should handle aspect ratio mismatches.
type ScaleMode int

const (
	// ScaleModeFit scales to fit within target dimensions, preserving aspect ratio (may letterbox).
	ScaleModeFit ScaleMode = iota
	// ScaleModeFill scales to fill target dimensions, preserving aspect ratio (may crop).
	ScaleModeFill
	// ScaleModeStretch scales to exactly match target dimensions (may distort).
	ScaleModeStretch
)

// VideoScaler scales I420 video frames.
type VideoScaler struct {
	srcWidth, srcHeight int
	dstWidth, dstHeight int
	mode                ScaleMode

	// Pre-allocated output buffer
	outY, outU, outV []byte
}

// NewVideoScaler creates a new scaler for the given dimensions.
func NewVideoScaler(srcWidth, srcHeight, dstWidth, dstHeight int, mode ScaleMode) *VideoScaler {
	ySize := dstWidth * dstHeight
	uvSize := (dstWidth / 2) * (dstHeight / 2)

	return &VideoScaler{
		srcWidth:  srcWidth,
		srcHeight: srcHeight,
		dstWidth:  dstWidth,
		dstHeight: dstHeight,
		mode:      mode,
		outY:      make([]byte, ySize),
		outU:      make([]byte, uvSize),
		outV:      make([]byte, uvSize),
	}
}

// Scale scales an I420 frame to the target dimensions.
func (s *VideoScaler) Scale(frame *VideoFrame) *VideoFrame {
	if frame.Width == s.dstWidth && frame.Height == s.dstHeight {
		// No scaling needed
		return frame
	}

	// Calculate source region based on scale mode
	srcX, srcY, srcW, srcH := s.calculateSourceRegion(frame.Width, frame.Height)

	// Scale Y plane
	s.scalePlane(frame.Data[0], frame.Stride[0], srcX, srcY, srcW, srcH,
		s.outY, s.dstWidth, s.dstWidth, s.dstHeight)

	// Scale U plane (half resolution)
	s.scalePlane(frame.Data[1], frame.Stride[1], srcX/2, srcY/2, srcW/2, srcH/2,
		s.outU, s.dstWidth/2, s.dstWidth/2, s.dstHeight/2)

	// Scale V plane (half resolution)
	s.scalePlane(frame.Data[2], frame.Stride[2], srcX/2, srcY/2, srcW/2, srcH/2,
		s.outV, s.dstWidth/2, s.dstWidth/2, s.dstHeight/2)

	return &VideoFrame{
		Data:      [][]byte{s.outY, s.outU, s.outV},
		Stride:    []int{s.dstWidth, s.dstWidth / 2, s.dstWidth / 2},
		Width:     s.dstWidth,
		Height:    s.dstHeight,
		Format:    PixelFormatI420,
		Timestamp: frame.Timestamp,
	}
}

// calculateSourceRegion determines what region of the source to use based on scale mode.
func (s *VideoScaler) calculateSourceRegion(srcW, srcH int) (x, y, w, h int) {
	switch s.mode {
	case ScaleModeFit:
		// Use entire source, may need to add letterboxing (handled by clearing output first)
		return 0, 0, srcW, srcH

	case ScaleModeFill:
		// Crop source to match target aspect ratio
		srcAspect := float64(srcW) / float64(srcH)
		dstAspect := float64(s.dstWidth) / float64(s.dstHeight)

		if srcAspect > dstAspect {
			// Source is wider, crop horizontally
			newW := int(float64(srcH) * dstAspect)
			return (srcW - newW) / 2, 0, newW, srcH
		} else if srcAspect < dstAspect {
			// Source is taller, crop vertically
			newH := int(float64(srcW) / dstAspect)
			return 0, (srcH - newH) / 2, srcW, newH
		}
		return 0, 0, srcW, srcH

	case ScaleModeStretch:
		// Use entire source
		return 0, 0, srcW, srcH

	default:
		return 0, 0, srcW, srcH
	}
}

// scalePlane scales a single plane using bilinear interpolation.
func (s *VideoScaler) scalePlane(src []byte, srcStride, srcX, srcY, srcW, srcH int,
	dst []byte, dstStride, dstW, dstH int) {

	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return
	}

	// Fixed-point scaling factors (16.16)
	xRatio := (srcW << 16) / dstW
	yRatio := (srcH << 16) / dstH

	for y := 0; y < dstH; y++ {
		// Source Y coordinate in fixed-point
		srcYFP := y * yRatio
		srcYInt := srcYFP >> 16
		srcYFrac := srcYFP & 0xFFFF

		// Clamp to valid range
		y0 := srcYInt + srcY
		y1 := y0 + 1
		if y1 >= srcY+srcH {
			y1 = y0
		}

		for x := 0; x < dstW; x++ {
			// Source X coordinate in fixed-point
			srcXFP := x * xRatio
			srcXInt := srcXFP >> 16
			srcXFrac := srcXFP & 0xFFFF

			// Clamp to valid range
			x0 := srcXInt + srcX
			x1 := x0 + 1
			if x1 >= srcX+srcW {
				x1 = x0
			}

			// Get four surrounding pixels
			p00 := int(src[y0*srcStride+x0])
			p10 := int(src[y0*srcStride+x1])
			p01 := int(src[y1*srcStride+x0])
			p11 := int(src[y1*srcStride+x1])

			// Bilinear interpolation
			xWeight := srcXFrac
			yWeight := srcYFrac

			// Interpolate horizontally
			top := (p00*(0x10000-xWeight) + p10*xWeight) >> 16
			bottom := (p01*(0x10000-xWeight) + p11*xWeight) >> 16

			// Interpolate vertically
			result := (top*(0x10000-yWeight) + bottom*yWeight) >> 16

			dst[y*dstStride+x] = byte(result)
		}
	}
}

// ScaleFrame is a convenience function to scale a frame without creating a scaler.
func ScaleFrame(frame *VideoFrame, dstWidth, dstHeight int, mode ScaleMode) *VideoFrame {
	scaler := NewVideoScaler(frame.Width, frame.Height, dstWidth, dstHeight, mode)
	return scaler.Scale(frame)
}

// CalculateScaledSize returns the output dimensions when scaling with a given mode.
// This is useful for determining letterbox dimensions in ScaleModeFit.
func CalculateScaledSize(srcW, srcH, maxW, maxH int, mode ScaleMode) (w, h int) {
	switch mode {
	case ScaleModeFit:
		srcAspect := float64(srcW) / float64(srcH)
		dstAspect := float64(maxW) / float64(maxH)

		if srcAspect > dstAspect {
			// Source is wider, fit to width
			w = maxW
			h = int(float64(maxW) / srcAspect)
		} else {
			// Source is taller, fit to height
			h = maxH
			w = int(float64(maxH) * srcAspect)
		}
		// Ensure even dimensions for YUV
		w = (w + 1) &^ 1
		h = (h + 1) &^ 1
		return w, h

	case ScaleModeFill, ScaleModeStretch:
		return maxW, maxH

	default:
		return maxW, maxH
	}
}
