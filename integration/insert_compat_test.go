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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/FerretDB/FerretDB/integration/setup"
)

type insertCompatTestCase struct {
	insert     []any                    // required, slice of bson.D to be insert
	ordered    bool                     // defaults to false
	resultType compatTestCaseResultType // defaults to nonEmptyResult
}

// testInsertCompat tests insert compatibility test cases.
func testInsertCompat(t *testing.T, testCases map[string]insertCompatTestCase) {
	t.Helper()

	for name, tc := range testCases {
		name, tc := name, tc

		t.Run(name, func(t *testing.T) {
			t.Helper()
			t.Parallel()

			t.Run("InsertOne", func(t *testing.T) {
				t.Helper()
				t.Parallel()

				ctx, targetCollections, compatCollections := setup.SetupCompat(t)

				insert := tc.insert
				require.NotEmpty(t, insert, "insert should be set")

				for i := range targetCollections {
					targetCollection := targetCollections[i]
					compatCollection := compatCollections[i]
					t.Run(targetCollection.Name(), func(t *testing.T) {
						t.Helper()

						for _, doc := range insert {
							targetInsertRes, targetErr := targetCollection.InsertOne(ctx, doc)
							compatInsertRes, compatErr := compatCollection.InsertOne(ctx, doc)

							if targetErr != nil {
								switch targetErr := targetErr.(type) { //nolint:errorlint // don't inspect error chain
								case mongo.WriteException:
									AssertMatchesWriteError(t, compatErr, targetErr)
								case mongo.BulkWriteException:
									AssertMatchesBulkException(t, compatErr, targetErr)
								default:
									assert.Equal(t, compatErr, targetErr)
								}

								continue
							}

							require.NoError(t, compatErr, "compat error; target returned no error")
							require.Equal(t, compatInsertRes, targetInsertRes)
						}

						targetFindRes := FindAll(t, ctx, targetCollection)
						compatFindRes := FindAll(t, ctx, compatCollection)

						require.Equal(t, len(compatFindRes), len(targetFindRes))

						for i := range compatFindRes {
							AssertEqualDocuments(t, compatFindRes[i], targetFindRes[i])
						}
					})
				}
			})

			t.Run("InsertMany", func(t *testing.T) {
				t.Helper()
				t.Parallel()

				ctx, targetCollections, compatCollections := setup.SetupCompat(t)

				insert := tc.insert
				require.NotEmpty(t, insert, "insert should be set")

				var nonEmptyResults bool
				for i := range targetCollections {
					targetCollection := targetCollections[i]
					compatCollection := compatCollections[i]
					t.Run(targetCollection.Name(), func(t *testing.T) {
						t.Helper()

						opts := options.InsertMany().SetOrdered(tc.ordered)
						targetInsertRes, targetErr := targetCollection.InsertMany(ctx, insert, opts)
						compatInsertRes, compatErr := compatCollection.InsertMany(ctx, insert, opts)

						// If the result contains inserted ids, we consider the result non-empty.
						if (compatInsertRes != nil && len(compatInsertRes.InsertedIDs) > 0) ||
							(targetInsertRes != nil && len(targetInsertRes.InsertedIDs) > 0) {
							nonEmptyResults = true
						}

						if targetErr != nil {
							switch targetErr := targetErr.(type) { //nolint:errorlint // don't inspect error chain
							case mongo.WriteException:
								AssertMatchesWriteError(t, compatErr, targetErr)
							case mongo.BulkWriteException:
								AssertMatchesBulkException(t, compatErr, targetErr)
							default:
								assert.Equal(t, compatErr, targetErr)
							}

							return
						}

						require.NoError(t, compatErr, "compat error; target returned no error")
						require.Equal(t, compatInsertRes, targetInsertRes)

						targetFindRes := FindAll(t, ctx, targetCollection)
						compatFindRes := FindAll(t, ctx, compatCollection)

						require.Equal(t, len(compatFindRes), len(targetFindRes))

						for i := range compatFindRes {
							AssertEqualDocuments(t, compatFindRes[i], targetFindRes[i])
						}
					})
				}

				switch tc.resultType {
				case nonEmptyResult:
					assert.True(t, nonEmptyResults, "expected non-empty results")
				case emptyResult:
					assert.False(t, nonEmptyResults, "expected empty results")
				default:
					t.Fatalf("unknown result type %v", tc.resultType)
				}
			})
		})
	}
}

func TestInsertCompat(t *testing.T) {
	t.Parallel()

	testCases := map[string]insertCompatTestCase{
		"Normal": {
			insert: []any{bson.D{{"_id", int32(42)}}},
		},

		"IDArray": {
			insert:     []any{bson.D{{"_id", bson.A{"foo", "bar"}}}},
			resultType: emptyResult,
		},
		"IDRegex": {
			insert:     []any{bson.D{{"_id", primitive.Regex{Pattern: "^regex$", Options: "i"}}}},
			resultType: emptyResult,
		},

		"OrderedAllErrors": {
			insert: []any{
				bson.D{{"_id", bson.A{"foo", "bar"}}},
				bson.D{{"_id", primitive.Regex{Pattern: "^regex$", Options: "i"}}},
			},
			ordered:    true,
			resultType: emptyResult,
		},
		"UnorderedAllErrors": {
			insert: []any{
				bson.D{{"_id", bson.A{"foo", "bar"}}},
				bson.D{{"_id", primitive.Regex{Pattern: "^regex$", Options: "i"}}},
			},
			ordered:    false,
			resultType: emptyResult,
		},

		"OrderedOneError": {
			insert: []any{
				bson.D{{"_id", "1"}},
				bson.D{{"_id", primitive.Regex{Pattern: "^regex$", Options: "i"}}},
				bson.D{{"_id", "2"}},
			},
			ordered: true,
		},
		"UnorderedOneError": {
			insert: []any{
				bson.D{{"_id", "1"}},
				bson.D{{"_id", "1"}}, // to test duplicate key error
				bson.D{{"_id", primitive.Regex{Pattern: "^regex$", Options: "i"}}},
				bson.D{{"_id", "2"}},
			},
			ordered: false,
		},
	}

	testInsertCompat(t, testCases)
}
