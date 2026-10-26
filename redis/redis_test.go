package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0x48core/gokit/metrics"
	"github.com/go-redis/redismock/v9"
	"github.com/stretchr/testify/suite"
)

type TestRedisCacheSuite struct {
	suite.Suite
	metricClient    metrics.Client
	cache           Client
	mockRedisClient redismock.ClusterClientMock
}

func (suite *TestRedisCacheSuite) SetupSuite() {
	suite.metricClient = metrics.NewClient("redis", "test")
}

func (suite *TestRedisCacheSuite) SetupTest() {
	redisCmd, redisMock := redismock.NewClusterMock()
	suite.mockRedisClient = redisMock
	suite.cache = newRedisClient(redisCmd, suite.metricClient)
}

func (suite *TestRedisCacheSuite) TearDownTest() {
	suite.NoError(suite.mockRedisClient.ExpectationsWereMet())
}

func TestTestRedisCacheSuite(t *testing.T) {
	suite.Run(t, new(TestRedisCacheSuite))
}

func (suite *TestRedisCacheSuite) TestNewRedisCache() {
	suite.NotNil(suite.cache)
}

func (suite *TestRedisCacheSuite) Test_Set() {
	// Arrange
	suite.mockRedisClient.ExpectSet("key", mockDataStr(), 2*time.Second).SetVal("OK")

	// Action
	err := suite.cache.Set(context.Background(), "caller", "key", mockDataStruct(), 2*time.Second)

	// Assert
	suite.NoError(err)
}

func (suite *TestRedisCacheSuite) Test_SetFailed() {
	// Arrange
	suite.mockRedisClient.ExpectSet("key", mockDataStr(), 2*time.Second).SetErr(errors.New("connection timeout"))

	// Action
	err := suite.cache.Set(context.Background(), "caller", "key", mockDataStruct(), 2*time.Second)

	// Assert
	suite.Error(err)
}

func (suite *TestRedisCacheSuite) Test_SetSerializeFailed() {
	// Action
	err := suite.cache.Set(context.Background(), "caller", "key", make(chan int), 2*time.Second)

	// Assert
	suite.Error(err)
}

func (suite *TestRedisCacheSuite) Test_Get() {
	// Arrange
	suite.mockRedisClient.ExpectGet("key").SetVal(mockDataStr())

	// Action
	var got Test
	err := suite.cache.Get(context.Background(), "caller", "key", &got)

	// Assert
	suite.NoError(err)
	suite.Equal(mockDataStruct(), got)
}

func (suite *TestRedisCacheSuite) Test_GetNotFound() {
	// Arrange
	suite.mockRedisClient.ExpectGet("key").RedisNil()

	// Action
	var got Test
	err := suite.cache.Get(context.Background(), "caller", "key", &got)

	// Assert
	suite.ErrorIs(err, ErrNotFound)
}

func (suite *TestRedisCacheSuite) Test_GetDeserializeFailed() {
	// Arrange
	suite.mockRedisClient.ExpectGet("key").SetVal("not json")

	// Action
	var got Test
	err := suite.cache.Get(context.Background(), "caller", "key", &got)

	// Assert
	suite.Error(err)
}

func (suite *TestRedisCacheSuite) Test_Delete() {
	// Arrange
	suite.mockRedisClient.ExpectDel("key").SetVal(1)

	// Action
	err := suite.cache.Delete(context.Background(), "caller", "key")

	// Assert
	suite.NoError(err)
}

func (suite *TestRedisCacheSuite) Test_DeleteFailed() {
	// Arrange
	suite.mockRedisClient.ExpectDel("key").SetErr(errors.New("connection timeout"))

	// Action
	err := suite.cache.Delete(context.Background(), "caller", "key")

	// Assert
	suite.Error(err)
}

func (suite *TestRedisCacheSuite) Test_SetNX() {
	// Arrange
	mockStruct := mockDataStruct()
	mockStr := mockDataStr()

	suite.mockRedisClient.ExpectSetNX("key", mockStr, 2*time.Second).SetVal(true)

	// Action
	ok, err := suite.cache.SetNx(context.Background(), "caller", "key", mockStruct, 2*time.Second)

	// Assert
	suite.NoError(err)
	suite.True(ok)
}

func (suite *TestRedisCacheSuite) Test_SetNXFailed() {
	// Arrange
	mockStruct := mockDataStruct()
	mockStr := mockDataStr()

	suite.mockRedisClient.ExpectSetNX("key", mockStr, 2*time.Second).SetVal(false)

	// Action
	ok, err := suite.cache.SetNx(context.Background(), "caller", "key", mockStruct, 2*time.Second)

	// Assert
	suite.NoError(err)
	suite.False(ok)
}

func (suite *TestRedisCacheSuite) Test_GetTTLByKey_Failed() {
	// Arrange
	suite.mockRedisClient.ExpectTTL("key_test").SetErr(errors.New("connection timeout"))

	// Action
	_, err := suite.cache.GetTTLByKey(context.Background(), "caller", "key_test")

	// Assert
	suite.Error(err)
}

func (suite *TestRedisCacheSuite) Test_GetTTLByKey() {
	// Arrange
	suite.mockRedisClient.ExpectTTL("key_test").SetVal(20 * time.Second)

	// Action
	duration, err := suite.cache.GetTTLByKey(context.Background(), "caller", "key_test")

	// Assert
	suite.NoError(err)
	suite.Equal(int64(20), int64(duration/time.Second))
}

// Define data test
type Test struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func mockDataStr() string {
	return `{"id":1,"name":"test"}`
}

func mockDataStruct() Test {
	return Test{
		ID:   1,
		Name: "test",
	}
}
