// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"net"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/FerretDB/FerretDB/integration/setup"
	"github.com/FerretDB/FerretDB/integration/shareddata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
	"github.com/FerretDB/FerretDB/internal/util/testutil/teststress"
)

func TestCommandsDiagnosticConnectionStatus(t *testing.T) {
	t.Parallel()
	ctx, collection := setup.Setup(t)

	var actual bson.D
	err := collection.Database().RunCommand(ctx, bson.D{{"connectionStatus", "*"}}).Decode(&actual)
	require.NoError(t, err)

	ok := actual.Map()["ok"]

	assert.Equal(t, float64(1), ok)
}

func TestCommandsDiagnosticExplain(t *testing.T) {
	t.Parallel()
	s := setup.SetupWithOpts(t, &setup.SetupOpts{
		Providers: []shareddata.Provider{shareddata.Int32s},
	})
	ctx, collection := s.Ctx, s.Collection

	for name, tc := range map[string]struct {
		query   bson.D
		command bson.D
	}{
		"Count": {
			query:   bson.D{{"count", collection.Name()}},
			command: bson.D{{"count", collection.Name()}, {"$db", collection.Database().Name()}},
		},
		"Find": {
			query: bson.D{
				{"find", collection.Name()},
				{"filter", bson.D{{"v", bson.D{{"$gt", int32(0)}}}}},
			},
			command: bson.D{
				{"find", collection.Name()},
				{"filter", bson.D{{"v", bson.D{{"$gt", int32(0)}}}}},
				{"$db", collection.Database().Name()},
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(tt *testing.T) {
			tt.Parallel()

			t := setup.FailsForSQLite(tt, "https://github.com/FerretDB/FerretDB/issues/3050")

			var actual bson.D

			err := collection.Database().RunCommand(ctx, bson.D{{"explain", tc.query}}).Decode(&actual)
			require.NoError(t, err)

			explainResult := actual.Map()

			assert.Equal(t, float64(1), explainResult["ok"])
			assert.Equal(t, "1", explainResult["explainVersion"])
			assert.Equal(t, tc.command, explainResult["command"])

			serverInfo := ConvertDocument(t, explainResult["serverInfo"].(bson.D))
			keys := serverInfo.Keys()
			values := serverInfo.Values()

			var host string
			var gitVersion string
			var version string

			for i, k := range keys {
				switch k {
				case "host":
					host = values[i].(string)
				case "gitVersion":
					gitVersion = values[i].(string)
				case "version":
					version = values[i].(string)
				}
			}

			assert.NotEmpty(t, host)

			assert.NotEmpty(t, gitVersion)
			assert.Regexp(t, `^6\.0\.`, version)

			assert.NotEmpty(t, explainResult["queryPlanner"])
			assert.IsType(t, bson.D{}, explainResult["queryPlanner"])
		})
	}
}

func TestCommandsDiagnosticGetLog(t *testing.T) {
	t.Parallel()
	res := setup.SetupWithOpts(t, &setup.SetupOpts{
		DatabaseName: "admin",
	})

	ctx, collection := res.Ctx, res.Collection

	for name, tc := range map[string]struct {
		command bson.D // required, command to run

		expected   map[string]any      // optional, expected keys of response
		err        *mongo.CommandError // optional, expected error from MongoDB
		altMessage string              // optional, alternative error message for FerretDB, ignored if empty
		skip       string              // optional, skip test with a specified reason
	}{
		"Asterisk": {
			command: bson.D{{"getLog", "*"}},
			expected: map[string]any{
				"names": bson.A(bson.A{"global", "startupWarnings"}),
				"ok":    float64(1),
			},
		},
		"Global": {
			command: bson.D{{"getLog", "global"}},
			expected: map[string]any{
				"totalLinesWritten": int64(1024),
				"log":               bson.A{},
				"ok":                float64(1),
			},
		},
		"StartupWarnings": {
			command: bson.D{{"getLog", "startupWarnings"}},
			expected: map[string]any{
				"totalLinesWritten": int64(1024),
				"log":               bson.A{},
				"ok":                float64(1),
			},
		},
		"NonExistentName": {
			command: bson.D{{"getLog", "nonExistentName"}},
			err: &mongo.CommandError{
				Code:    96,
				Name:    "OperationFailed",
				Message: `No log named 'nonExistentName'`,
			},
			altMessage: `no RecentEntries named: nonExistentName`,
		},
		"Nil": {
			command: bson.D{{"getLog", nil}},
			err: &mongo.CommandError{
				Code:    40414,
				Name:    "Location40414",
				Message: `BSON field 'getLog.getLog' is missing but a required field`,
			},
		},
		"Array": {
			command: bson.D{{"getLog", bson.A{}}},
			err: &mongo.CommandError{
				Code:    14,
				Name:    "TypeMismatch",
				Message: `BSON field 'getLog.getLog' is the wrong type 'array', expected type 'string'`,
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(tt *testing.T) {
			if tc.skip != "" {
				tt.Skip(tc.skip)
			}

			tt.Parallel()

			t := setup.FailsForSQLite(tt, "https://github.com/FerretDB/FerretDB/issues/3261")

			require.NotNil(t, tc.command, "command must not be nil")

			var res bson.D
			err := collection.Database().RunCommand(ctx, tc.command).Decode(&res)
			if tc.err != nil {
				assert.Nil(t, res)
				AssertEqualAltCommandError(t, *tc.err, tc.altMessage, err)

				return
			}

			require.NoError(t, err)

			m := res.Map()
			k := CollectKeys(t, res)

			for key, item := range tc.expected {
				assert.Contains(t, k, key)
				if key != "log" && key != "totalLinesWritten" {
					assert.Equal(t, m[key], item)
				}
			}
		})
	}
}

func TestCommandsDiagnosticHostInfo(t *testing.T) {
	t.Parallel()
	ctx, collection := setup.Setup(t)

	var actual bson.D
	err := collection.Database().RunCommand(ctx, bson.D{{"hostInfo", 42}}).Decode(&actual)
	require.NoError(t, err)

	m := actual.Map()
	t.Log(m)

	assert.Equal(t, float64(1), m["ok"])
	assert.Equal(t, []string{"system", "os", "extra", "ok"}, CollectKeys(t, actual))

	os := m["os"].(bson.D)
	assert.Equal(t, []string{"type", "name", "version"}, CollectKeys(t, os))

	system := m["system"].(bson.D)
	keys := CollectKeys(t, system)
	assert.Contains(t, keys, "currentTime")
	assert.Contains(t, keys, "hostname")
	assert.Contains(t, keys, "cpuAddrSize")
	assert.Contains(t, keys, "numCores")
	assert.Contains(t, keys, "cpuArch")
}

func TestCommandsDiagnosticListCommands(t *testing.T) {
	t.Parallel()
	ctx, collection := setup.Setup(t)

	var actual bson.D
	err := collection.Database().RunCommand(ctx, bson.D{{"listCommands", 42}}).Decode(&actual)
	require.NoError(t, err)

	m := actual.Map()
	t.Log(m)

	assert.Equal(t, float64(1), m["ok"])
	assert.Equal(t, []string{"commands", "ok"}, CollectKeys(t, actual))

	commands := m["commands"].(bson.D)
	listCommands := commands.Map()["listCommands"].(bson.D)
	assert.NotEmpty(t, listCommands.Map()["help"].(string))
}

func TestCommandsDiagnosticValidate(t *testing.T) {
	t.Parallel()
	ctx, collection := setup.Setup(t, shareddata.Doubles)

	var doc bson.D
	err := collection.Database().RunCommand(ctx, bson.D{{"validate", collection.Name()}}).Decode(&doc)
	require.NoError(t, err)

	t.Log(doc.Map())

	actual := ConvertDocument(t, doc)
	expected := must.NotFail(types.NewDocument(
		"ns", "TestCommandsDiagnosticValidate.TestCommandsDiagnosticValidate",
		"nInvalidDocuments", int32(0),
		"nNonCompliantDocuments", int32(0),
		"nrecords", int32(0), // replaced below
		"nIndexes", int32(1),
		"valid", true,
		"repaired", false,
		"warnings", types.MakeArray(0),
		"errors", types.MakeArray(0),
		"extraIndexEntries", types.MakeArray(0),
		"missingIndexEntries", types.MakeArray(0),
		"corruptRecords", types.MakeArray(0),
		"ok", float64(1),
	))

	actual.Remove("keysPerIndex")
	actual.Remove("indexDetails")
	testutil.CompareAndSetByPathNum(t, expected, actual, 39, types.NewStaticPath("nrecords"))
	testutil.AssertEqual(t, expected, actual)
}

func TestCommandsDiagnosticValidateError(t *testing.T) {
	t.Skip("https://github.com/FerretDB/FerretDB/issues/2704")

	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // for readability
		command bson.D
		err     *mongo.CommandError
	}{
		"InvalidTypeDocument": {
			command: bson.D{{"validate", bson.D{}}},
			err: &mongo.CommandError{
				Code:    73,
				Name:    "InvalidNamespace",
				Message: "collection name has invalid type object",
			},
		},
		"NonExistentCollection": {
			command: bson.D{{"validate", "nonExistentCollection"}},
			err: &mongo.CommandError{
				Code:    26,
				Name:    "NamespaceNotFound",
				Message: "Collection 'TestCommandsDiagnosticValidateError-NonExistentCollection.nonExistentCollection' does not exist to validate.",
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NotNil(t, tc.command, "command must not be nil")
			require.NotNil(t, tc.err, "err must not be nil")

			ctx, collection := setup.Setup(t, shareddata.Doubles)

			var res bson.D
			err := collection.Database().RunCommand(ctx, tc.command).Decode(res)

			assert.Nil(t, res)
			AssertEqualCommandError(t, *tc.err, err)
		})
	}
}

func TestCommandsDiagnosticWhatsMyURI(t *testing.T) {
	t.Parallel()

	s := setup.SetupWithOpts(t, nil)
	collection1 := s.Collection
	databaseName := s.Collection.Database().Name()
	collectionName := s.Collection.Name()

	// only check port number on TCP connection, no need to check on Unix socket
	isUnix := s.IsUnixSocket(t)

	// setup second client connection to check that `whatsmyuri` returns different ports
	client2, err := mongo.Connect(s.Ctx, options.Client().ApplyURI(s.MongoDBURI))
	require.NoError(t, err)

	defer client2.Disconnect(s.Ctx)

	collection2 := client2.Database(databaseName).Collection(collectionName)

	var ports []string

	for _, collection := range []*mongo.Collection{collection1, collection2} {
		var actual bson.D
		command := bson.D{{"whatsmyuri", int32(1)}}
		err := collection.Database().RunCommand(s.Ctx, command).Decode(&actual)
		require.NoError(t, err)

		doc := ConvertDocument(t, actual)
		keys := doc.Keys()
		values := doc.Values()

		var ok float64
		var you string

		for i, k := range keys {
			switch k {
			case "ok":
				ok = values[i].(float64)
			case "you":
				you = values[i].(string)
			}
		}

		assert.Equal(t, float64(1), ok)

		if !isUnix {
			// record ports to compare that they are not equal for two different clients.
			_, port, err := net.SplitHostPort(you)
			require.NoError(t, err)
			assert.NotEmpty(t, port)
			ports = append(ports, port)
		}
	}

	if !isUnix {
		require.Equal(t, 2, len(ports))
		assert.NotEqual(t, ports[0], ports[1])
	}
}

// TestCommandWhatsMyURIConnection tests that integration test setup applies
// minPoolSize, maxPoolSize and maxIdleTimeMS correctly to the driver.
// It also tests that the driver behaves like we think it should.
func TestCommandWhatsMyURIConnection(t *testing.T) {
	t.Parallel()

	// options are applied to create a client that uses single connection pool
	s := setup.SetupWithOpts(t, &setup.SetupOpts{
		ExtraOptions: url.Values{
			"minPoolSize":   []string{"1"},
			"maxPoolSize":   []string{"1"},
			"maxIdleTimeMS": []string{"0"},
		},
	})

	collection1 := s.Collection
	databaseName := s.Collection.Database().Name()
	collectionName := s.Collection.Name()

	t.Run("SameClientStress", func(t *testing.T) {
		t.Parallel()

		ports := make(chan string, teststress.NumGoroutines)

		teststress.Stress(t, func(ready chan<- struct{}, start <-chan struct{}) {
			ready <- struct{}{}
			<-start

			var res bson.D
			err := collection1.Database().RunCommand(s.Ctx, bson.D{{"whatsmyuri", int32(1)}}).Decode(&res)
			require.NoError(t, err)

			doc := ConvertDocument(t, res)
			v, _ := doc.Get("ok")
			resOk, ok := v.(float64)
			require.True(t, ok)
			assert.Equal(t, float64(1), resOk)

			v, _ = doc.Get("you")
			you, ok := v.(string)
			require.True(t, ok)

			_, port, err := net.SplitHostPort(you)
			require.NoError(t, err)
			assert.NotEmpty(t, port)
			ports <- port
		})

		close(ports)

		firstPort := <-ports
		for port := range ports {
			require.Equal(t, firstPort, port, "expected same client to use the same port")
		}
	})

	t.Run("DifferentClient", func(t *testing.T) {
		t.Parallel()

		u, err := url.Parse(s.MongoDBURI)
		require.NoError(t, err)

		client2, err := mongo.Connect(s.Ctx, options.Client().ApplyURI(u.String()))
		require.NoError(t, err)

		defer client2.Disconnect(s.Ctx)

		collection2 := client2.Database(databaseName).Collection(collectionName)

		var ports []string

		for _, collection := range []*mongo.Collection{collection1, collection2} {
			var res bson.D
			err := collection.Database().RunCommand(s.Ctx, bson.D{{"whatsmyuri", int32(1)}}).Decode(&res)
			require.NoError(t, err)

			doc := ConvertDocument(t, res)
			v, _ := doc.Get("ok")
			resOk, ok := v.(float64)
			require.True(t, ok)
			assert.Equal(t, float64(1), resOk)

			v, _ = doc.Get("you")
			you, ok := v.(string)
			require.True(t, ok)

			_, port, err := net.SplitHostPort(you)
			require.NoError(t, err)
			assert.NotEmpty(t, port)

			ports = append(ports, port)
		}

		require.Equal(t, 2, len(ports))
		assert.NotEqual(t, ports[0], ports[1])
	})
}
