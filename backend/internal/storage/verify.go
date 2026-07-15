package storage

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

func (s *Store) VerifyManifest(ctx context.Context, reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	line := 0
	for scanner.Scan() {
		line++
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 5 {
			return fmt.Errorf("对象清单第 %d 行无效", line)
		}
		scope, path, thumb := fields[1], fields[2], fields[3]
		if err := s.Exists(ctx, scope, path); err != nil {
			return fmt.Errorf("对象清单第 %d 行原图缺失: %w", line, err)
		}
		if thumb != "" {
			if err := s.Exists(ctx, scope, thumb); err != nil {
				return fmt.Errorf("对象清单第 %d 行缩略图缺失: %w", line, err)
			}
		}
	}
	return scanner.Err()
}
