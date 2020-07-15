package main

import (
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

var (
	cloudwatchSemaphore chan struct{}
	tagSemaphore        chan struct{}
)

func scrapeAwsData(config conf) ([]*tagsData, []*cloudwatchData) {
	mux := &sync.Mutex{}

	cwData := make([]*cloudwatchData, 0)
	awsInfoData := make([]*tagsData, 0)

	var wg sync.WaitGroup

	for _, job := range config.Discovery.Jobs {
		for _, roleArn := range job.RoleArns {
			for _, region := range job.Regions {
				wg.Add(1)

				go func(region string, roleArn string) {
					defer wg.Done()
					log.Infof("Assuming sessions with %s role in %s region", region, roleArn)
					clientCloudwatch := cloudwatchInterface{
						client: createCloudwatchSession(&region, roleArn),
					}
					clientTag := tagsInterface{
						client:    createTagSession(&region, roleArn),
						asgClient: createASGSession(&region, roleArn),
					}
					var resources []*tagsData
					var metrics []*cloudwatchData
					resources, metrics = scrapeDiscoveryJobUsingMetricData(job, region, config.Discovery.ExportedTagsOnMetrics, clientTag, clientCloudwatch)
					mux.Lock()
					awsInfoData = append(awsInfoData, resources...)
					cwData = append(cwData, metrics...)
					mux.Unlock()
				}(region, roleArn)
			}
		}
	}

	for _, job := range config.Static {
		for _, roleArn := range job.RoleArns {
			for _, region := range job.Regions {
				wg.Add(1)

				go func(region string, roleArn string) {
					clientCloudwatch := cloudwatchInterface{
						client: createCloudwatchSession(&region, roleArn),
					}

					metrics := scrapeStaticJob(job, region, clientCloudwatch)

					mux.Lock()
					cwData = append(cwData, metrics...)
					mux.Unlock()

					wg.Done()
				}(region, roleArn)
			}
		}
	}
	wg.Wait()
	log.Infof("awsInfoData length=%d, cwData length=%d\n", len(awsInfoData), len(cwData))
	return awsInfoData, cwData
}

func scrapeStaticJob(resource static, region string, clientCloudwatch cloudwatchInterface) (cw []*cloudwatchData) {
	mux := &sync.Mutex{}
	var wg sync.WaitGroup

	for j := range resource.Metrics {
		metric := resource.Metrics[j]
		wg.Add(1)
		go func() {
			defer wg.Done()

			cloudwatchSemaphore <- struct{}{}
			defer func() {
				<-cloudwatchSemaphore
			}()

			id := resource.Name
			service := strings.TrimPrefix(resource.Namespace, "AWS/")
			data := cloudwatchData{
				ID:                     &id,
				Metric:                 &metric.Name,
				Service:                &service,
				Statistics:             metric.Statistics,
				NilToZero:              &metric.NilToZero,
				AddCloudwatchTimestamp: &metric.AddCloudwatchTimestamp,
				CustomTags:             resource.CustomTags,
				Dimensions:             createStaticDimensions(resource.Dimensions),
				Region:                 &region,
			}

			filter := createGetMetricStatisticsInput(
				data.Dimensions,
				&resource.Namespace,
				metric,
			)

			data.Points = clientCloudwatch.get(filter)

			if data.Points != nil {
				mux.Lock()
				cw = append(cw, &data)
				mux.Unlock()
			}
		}()
	}
	wg.Wait()
	return cw
}

func scrapeDiscoveryJobUsingMetricData(
	job job,
	region string,
	tagsOnMetrics exportedTagsOnMetrics,
	clientTag tagsInterface,
	clientCloudwatch cloudwatchInterface) (awsInfoData []*tagsData, cw []*cloudwatchData) {
	var resources []*tagsData

	mux := &sync.Mutex{}
	var wg sync.WaitGroup
	var getMetricDatas []cloudwatchData
	var length int

	// Why is this here? 120?
	if job.Length == 0 {
		length = 120
	} else {
		length = job.Length
	}

	tagSemaphore <- struct{}{}
	commonResources, err := clientTag.get(job, region)
	if job.Type == "acm-certificates" {
		log.Infof("job: %s, clientTag.get returned %d resources and %v err", job.Type, len(resources), err)
	}
	<-tagSemaphore

	// Add the info tags of all the resources
	for _, resource := range resources {
		mux.Lock()
		awsInfoData = append(awsInfoData, resource)
		mux.Unlock()
	}

	if err != nil {
		log.Printf("Couldn't describe resources for region %s: %s\n", region, err.Error())
		return
	}
	// Get the awsDimensions of the job configuration
	// Common for all the metrics of the job
	commonJobDimensions := getAwsDimensions(job)
	if job.Type == "acm-certificates" {
		log.Infof("job.Type: %s, job.Metrics: %v, commonJobDimensions: %v", job.Type, job.Metrics, commonJobDimensions)
	}

	// For every metric of the job
	for j := range job.Metrics {
		metric := job.Metrics[j]

		if metric.Length > length {
			length = metric.Length
		}

		// Get the full list of metrics
		// This includes, for this metric the possible combinations
		// of dimensions and value of dimensions with data
		tagSemaphore <- struct{}{}
		fullMetricsList := getFullMetricsList(&job.Type, metric, clientCloudwatch)
		<-tagSemaphore
		if job.Type == "acm-certificates" {
			log.Infof("job: %s, metric: %v, fullMetricsList: %v", job.Type, metric, fullMetricsList)
		}
		if len(commonResources) == 0 {
			if job.Type == "acm-certificates" {
				log.Info("NO commonresources, fetching resources from detectResourcesByService")
			}
			resources = detectResourcesByService(job.Type, region, fullMetricsList.Metrics)
		} else {
			log.Info("GOING WITH commonresources")
			resources = commonResources
		}

		// For every resource
		for i := range resources {
			resource := resources[i]
			metricTags := resource.metricTags(tagsOnMetrics)
			if job.Type == "acm-certificates" {
				log.Infof("job: %s, resource: %v/%v, metricTags: %v", job.Type, *resource.Service, *resource.ID, metricTags)
			}

			// Creates the dimensions with values for the resource depending on the namespace of the job (p.e. InstanceId=XXXXXXX)
			dimensionsWithValue := detectDimensionsByService(resource.Service, resource.ID, fullMetricsList)

			// Adds the dimensions with values of that specific metric of the job
			dimensionsWithValue = addAdditionalDimensions(dimensionsWithValue, metric.AdditionalDimensions)
			if job.Type == "acm-certificates" {
				log.Infof("job: %s, resource: %v/%v, dimensionsWithValue: %v", job.Type, *resource.Service, *resource.ID, dimensionsWithValue)
			}

			// Filter the commonJob Dimensions by the discovered/added dimensions as duplicates cause no metrics to be discovered
			commonJobDimensions = filterDimensionsWithoutValueByDimensionsWithValue(commonJobDimensions, dimensionsWithValue)

			metricsToAdd := filterMetricsBasedOnDimensionsWithValues(dimensionsWithValue, commonJobDimensions, fullMetricsList)
			if job.Type == "acm-certificates" {
				log.Infof("job: %s, resource: %v/%v, metricsToAdd: %v", job.Type, *resource.Service, *resource.ID, metricsToAdd)
			}

			// If the job property inlyInfoIfData is true
			if metricsToAdd != nil {
				for _, fetchedMetrics := range metricsToAdd.Metrics {
					for _, stats := range metric.Statistics {
						if job.Type == "acm-certificates" {
							log.Infof("job: %s, resource: %v/%v, fetchedMetrics: %v, stats: %v", job.Type, *resource.Service, *resource.ID, fetchedMetrics, stats)
						}
						id := fmt.Sprintf("id_%d", rand.Int())
						period := int64(metric.Period)
						mux.Lock()
						getMetricDatas = append(getMetricDatas, cloudwatchData{
							ID:                     resource.ID,
							MetricID:               &id,
							Metric:                 &metric.Name,
							Service:                resource.Service,
							Statistics:             []string{stats},
							NilToZero:              &metric.NilToZero,
							AddCloudwatchTimestamp: &metric.AddCloudwatchTimestamp,
							Tags:                   metricTags,
							CustomTags:             job.CustomTags,
							Dimensions:             fetchedMetrics.Dimensions,
							Region:                 &region,
							Period:                 &period,
						})
						mux.Unlock()
					}
				}
			}
		}
	}
	wg.Wait()
	maxMetricCount := *metricsPerQuery
	metricDataLength := len(getMetricDatas)
	partition := int(math.Ceil(float64(metricDataLength) / float64(maxMetricCount)))
	if job.Type == "acm-certificates" {
		log.Infof("job.Type: %s, metricDataLength: %d, maxMetricCount: %d, partition: %d", job.Type, metricDataLength, maxMetricCount, partition)
	}
	wg.Add(partition)
	for i := 0; i < metricDataLength; i += maxMetricCount {
		go func(i int) {
			defer wg.Done()
			end := i + maxMetricCount
			if end > metricDataLength {
				end = metricDataLength
			}
			filter := createGetMetricDataInput(
				getMetricDatas[i:end],
				getNamespace(resources[0].Service),
				length,
				job.Delay,
			)

			data := clientCloudwatch.getMetricData(filter)
			if data != nil {
				for _, MetricDataResult := range data.MetricDataResults {
					getMetricData, err := findGetMetricDataById(getMetricDatas[i:end], *MetricDataResult.Id)
					if err == nil {
						if len(MetricDataResult.Values) != 0 {
							getMetricData.GetMetricDataPoint = MetricDataResult.Values[0]
							getMetricData.GetMetricDataTimestamps = MetricDataResult.Timestamps[0]
						}
						log.Infof("job.Type: %s, adding result into cw", job.Type)
						mux.Lock()
						cw = append(cw, &getMetricData)
						mux.Unlock()
					} else {
						log.Warningf("job.Type: %s, findMetricDataById failed due to %v", job.Type, err)
					}
				}
			} else {
				log.Infof("job.type: %s. No metric data found on %v", job.Type, filter)
			}
		}(i)
	}
	wg.Wait()
	log.Infof("scraped awsInfoData=%d, cw=%v", len(awsInfoData), len(cw))
	return awsInfoData, cw
}

func (r tagsData) filterThroughTags(filterTags []tag) bool {
	tagMatches := 0

	for _, resourceTag := range r.Tags {
		for _, filterTag := range filterTags {
			if resourceTag.Key == filterTag.Key {
				r, _ := regexp.Compile(filterTag.Value)
				if r.MatchString(resourceTag.Value) {
					tagMatches++
				}
			}
		}
	}

	return tagMatches == len(filterTags)
}

func (r tagsData) metricTags(tagsOnMetrics exportedTagsOnMetrics) []tag {
	tags := make([]tag, 0)
	for _, tagName := range tagsOnMetrics[*r.Service] {
		tag := tag{
			Key: tagName,
		}
		for _, resourceTag := range r.Tags {
			if resourceTag.Key == tagName {
				tag.Value = resourceTag.Value
				break
			}
		}

		// Always add the tag, even if it's empty, to ensure the same labels are present on all metrics for a single service
		tags = append(tags, tag)
	}
	return tags
}
