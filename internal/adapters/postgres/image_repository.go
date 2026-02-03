package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/worldland/worldland-hub/internal/domain"
)

// PostgresImageRepository implements domain.ImageRepository
type PostgresImageRepository struct {
	pool *pgxpool.Pool
}

// Compile-time interface check
var _ domain.ImageRepository = (*PostgresImageRepository)(nil)

// NewImageRepository creates a new PostgreSQL image repository
func NewImageRepository(pool *pgxpool.Pool) *PostgresImageRepository {
	return &PostgresImageRepository{pool: pool}
}

// GetByID retrieves a base image by its ID
func (r *PostgresImageRepository) GetByID(ctx context.Context, id string) (*domain.BaseImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, name, docker_image, category, gpu_required, description, created_at
		FROM base_images
		WHERE id = $1
	`

	var image domain.BaseImage
	var description *string
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&image.ID,
		&image.Name,
		&image.DockerImage,
		&image.Category,
		&image.GPURequired,
		&description,
		&image.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get base image by ID: %w", err)
	}

	if description != nil {
		image.Description = *description
	}

	return &image, nil
}

// List retrieves all active base images
func (r *PostgresImageRepository) List(ctx context.Context) ([]*domain.BaseImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, name, docker_image, category, gpu_required, description, created_at
		FROM base_images
		ORDER BY category, name
	`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list base images: %w", err)
	}
	defer rows.Close()

	var images []*domain.BaseImage
	for rows.Next() {
		var image domain.BaseImage
		var description *string
		err := rows.Scan(
			&image.ID,
			&image.Name,
			&image.DockerImage,
			&image.Category,
			&image.GPURequired,
			&description,
			&image.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan base image: %w", err)
		}

		if description != nil {
			image.Description = *description
		}

		images = append(images, &image)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating base images: %w", err)
	}

	return images, nil
}

// ListByCategory retrieves base images filtered by category
func (r *PostgresImageRepository) ListByCategory(ctx context.Context, category domain.ImageCategory) ([]*domain.BaseImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, name, docker_image, category, gpu_required, description, created_at
		FROM base_images
		WHERE category = $1
		ORDER BY name
	`

	rows, err := r.pool.Query(ctx, query, string(category))
	if err != nil {
		return nil, fmt.Errorf("failed to list base images by category: %w", err)
	}
	defer rows.Close()

	var images []*domain.BaseImage
	for rows.Next() {
		var image domain.BaseImage
		var description *string
		err := rows.Scan(
			&image.ID,
			&image.Name,
			&image.DockerImage,
			&image.Category,
			&image.GPURequired,
			&description,
			&image.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan base image: %w", err)
		}

		if description != nil {
			image.Description = *description
		}

		images = append(images, &image)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating base images: %w", err)
	}

	return images, nil
}
