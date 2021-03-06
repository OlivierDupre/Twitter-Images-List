package images

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/vision/apiv1"
	"github.com/aubm/twitter-image/images-api/shared"
	"github.com/satori/go.uuid"
	"golang.org/x/net/context"
	"google.golang.org/api/option"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/search"
	pb "google.golang.org/genproto/googleapis/cloud/vision/v1"
)

type Indexer struct {
	Logger     shared.LoggerInterface `inject:""`
	Config     *shared.AppConfig      `inject:""`
	HttpClient interface {
		Provide(ctx context.Context) *http.Client
	} `inject:""`
}

func (i *Indexer) Index(ctx context.Context, data IndexRequest) error {
	newImage := i.newImageFromIndexRequest(data)
	if err := i.annotateImageWithTags(ctx, newImage); err != nil {
		return fmt.Errorf("failed to compute the new image tags: %v", err)
	}
	if err := i.putToDatastore(ctx, newImage); err != nil {
		return fmt.Errorf("failed to put the new image into datastore: %v", err)
	}
	if err := i.putToSearchIndex(ctx, newImage); err != nil {
		return fmt.Errorf("failed to add the new image to the search index: %v", err)
	}
	return nil
}

func (i *Indexer) newImageFromIndexRequest(data IndexRequest) *Image {
	return &Image{
		ID:          uuid.NewV4().String(),
		Url:         data.Url,
		Description: data.Description,
		Tags:        []string{},
		CreatedAt:   time.Now(),
	}
}

func (i *Indexer) annotateImageWithTags(ctx context.Context, image *Image) error {
	visionClient, err := vision.NewImageAnnotatorClient(ctx, option.WithAPIKey(i.Config.VisionAPIKey))
	if err != nil {
		return fmt.Errorf("failed to instanciate the vision client: %v", err)
	}
	defer visionClient.Close()

	r, err := i.readUrl(ctx, image.Url)
	if err != nil {
		return fmt.Errorf("failed to read image url %v: %v", image.Url, err)
	}

	src, err := vision.NewImageFromReader(r)
	if err != nil {
		return fmt.Errorf("failed to create source image: %v", err)
	}

	annotations, err := visionClient.DetectLabels(ctx, src, &pb.ImageContext{},10)
	if err != nil {
		return fmt.Errorf("failed to detect image labels: %v", err)
	}

	tags := make([]string, 0)
	for _, annotation := range annotations {
		tags = append(tags, annotation.Description)
	}

	i.Logger.Infof(ctx, "For image: %v, found tags: %v", image.Url, tags)
	image.Tags = tags

	return nil
}

func (i *Indexer) readUrl(ctx context.Context, url string) (io.ReadCloser, error) {
	resp, err := i.HttpClient.Provide(ctx).Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to download image at url %v: %v", url, err)
	}
	if resp.StatusCode < 200 && resp.StatusCode >= 400 {
		return nil, fmt.Errorf("failed to download image at url %v, response code is %v", url, resp.StatusCode)
	}
	return resp.Body, nil
}

func (i *Indexer) putToDatastore(ctx context.Context, image *Image) error {
	if _, err := datastore.Put(ctx, buildKeyForImageID(ctx, image.ID), image); err != nil {
		return fmt.Errorf("datastore operation failed: %v", err)
	}
	return nil
}

func (i *Indexer) putToSearchIndex(ctx context.Context, image *Image) error {
	index, err := search.Open(searchIndexName)
	if err != nil {
		return fmt.Errorf("failed to open the search index: %v", err)
	}
	if _, err := index.Put(ctx, image.ID, &searchImageEntry{
		Tags:      strings.Join(image.Tags, ", "),
		CreatedAt: image.CreatedAt,
	}); err != nil {
		return fmt.Errorf("failed to put the image to index: %v", err)
	}
	return nil
}

type IndexRequest struct {
	Url         string `json:"url"`
	Description string `json:"description"`
}

type searchImageEntry struct {
	Tags      string
	CreatedAt time.Time
}
