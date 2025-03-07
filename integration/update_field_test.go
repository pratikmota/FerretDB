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
	"math"
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
)

func TestUpdateFieldSet(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		id     string // optional, defaults to empty
		update bson.D // required, used for update parameter

		res     *mongo.UpdateResult // optional, expected response from update
		findRes bson.D              // optional, expected response from find
		skip    string              // optional, skip test with a specified reason
	}{
		"ArrayNil": {
			id:      "string",
			update:  bson.D{{"$set", bson.D{{"v", bson.A{nil}}}}},
			findRes: bson.D{{"_id", "string"}, {"v", bson.A{nil}}},
			res: &mongo.UpdateResult{
				MatchedCount:  1,
				ModifiedCount: 1,
				UpsertedCount: 0,
			},
		},
		"SetSameValueInt": {
			id:      "int32",
			update:  bson.D{{"$set", bson.D{{"v", int32(42)}}}},
			findRes: bson.D{{"_id", "int32"}, {"v", int32(42)}},
			res: &mongo.UpdateResult{
				MatchedCount:  1,
				ModifiedCount: 0,
				UpsertedCount: 0,
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			t.Parallel()

			require.NotNil(t, tc.update, "update should be set")

			ctx, collection := setup.Setup(t, shareddata.Scalars, shareddata.Composites)

			res, err := collection.UpdateOne(ctx, bson.D{{"_id", tc.id}}, tc.update)

			require.NoError(t, err)
			require.Equal(t, tc.res, res)

			var actual bson.D
			err = collection.FindOne(ctx, bson.D{{"_id", tc.id}}).Decode(&actual)
			require.NoError(t, err)
			AssertEqualDocuments(t, tc.findRes, actual)
		})
	}
}

func TestUpdateFieldSetUpdateManyUpsert(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for testing only
		filter bson.D                 // optional, defaults to bson.D{}
		update bson.D                 // required, used for update parameter
		opts   *options.UpdateOptions // optional

		findRes []bson.E // required, expected response from find without _id generated by upsert
		skip    string   // optional, skip test with a specified reason
	}{
		"QueryOperator": {
			filter:  bson.D{{"v", bson.D{{"$lt", 3}}}},
			update:  bson.D{{"$set", bson.D{{"new", "val"}}}},
			opts:    options.Update().SetUpsert(true),
			findRes: []bson.E{{"new", "val"}},
		},
		"NoQueryOperator": {
			filter:  bson.D{{"v", int32(4080)}},
			update:  bson.D{{"$set", bson.D{{"new", "val"}}}},
			opts:    options.Update().SetUpsert(true),
			findRes: []bson.E{{"v", int32(4080)}, {"new", "val"}},
		},
		"QueryOperatorIDQuery": {
			filter:  bson.D{{"_id", bson.D{{"$eq", 1}}}},
			update:  bson.D{{"$set", bson.D{{"new", "val"}}}},
			opts:    options.Update().SetUpsert(true),
			findRes: []bson.E{{"new", "val"}},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NotNil(t, tc.update, "update should be set")
			require.NotNil(t, tc.findRes, "findRes should be set")

			ctx, collection := setup.Setup(t, shareddata.Nulls)

			updateRes, err := collection.UpdateMany(ctx, tc.filter, tc.update, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, int64(0), updateRes.MatchedCount)
			assert.Equal(t, int64(0), updateRes.ModifiedCount)
			assert.Equal(t, int64(1), updateRes.UpsertedCount)
			require.NotNil(t, updateRes.UpsertedID)

			cursor, err := collection.Find(ctx, bson.D{{"_id", updateRes.UpsertedID}})
			require.NoError(t, err)

			var res []bson.D
			err = cursor.All(ctx, &res)
			require.NoError(t, err)

			expected := bson.D{{"_id", updateRes.UpsertedID}}
			for _, e := range tc.findRes {
				expected = append(expected, e)
			}

			AssertEqualDocumentsSlice(t, []bson.D{expected}, res)
		})
	}
}

func TestUpdateCommandUpsert(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for testing only
		updates bson.A // required, used for update parameter

		nMatched  int32             // optional
		nModified int32             // optional
		nUpserted int               // optional
		findRes   []bson.D          // required, expected response from find without _id generated by upsert
		err       *mongo.WriteError // optional, expected error from MongoDB
		skip      string            // optional, skip test with a specified reason
	}{
		"NoUpdateOperator": {
			updates: bson.A{
				bson.D{
					{"q", bson.D{{"v", bson.D{{"$lt", 3}}}}},
					{"u", bson.D{{"updateV", "val"}}},
					{"upsert", true},
				},
			},
			nMatched:  int32(1),
			nModified: int32(0),
			nUpserted: 1,
			findRes:   []bson.D{{{"v", nil}}, {{"updateV", "val"}}},
		},
		"NoUpdateOperatorEmptyQuery": {
			updates: bson.A{
				bson.D{
					{"q", bson.D{}},
					{"u", bson.D{{"updateV", "val"}}},
					{"upsert", true},
				},
			},
			nMatched:  int32(1),
			nModified: int32(1),
			nUpserted: 0,
		},
		"NoQueryOperator": {
			updates: bson.A{
				bson.D{
					{"q", bson.D{{"queryV", "v"}}},
					{"u", bson.D{{"$set", bson.D{{"updateV", "val"}}}}},
					{"upsert", true},
				},
			},
			nMatched:  int32(1),
			nModified: int32(0),
			nUpserted: 1,
			findRes:   []bson.D{{{"v", nil}}, {{"queryV", "v"}, {"updateV", "val"}}},
		},
		"NoUpdateOperatorNoQueryOperator": {
			updates: bson.A{
				bson.D{
					{"q", bson.D{{"queryV", "val"}}},
					{"u", bson.D{{"updateV", "val"}}},
					{"upsert", true},
				},
			},
			nMatched:  int32(1),
			nModified: int32(0),
			nUpserted: 1,
			findRes:   []bson.D{{{"v", nil}}, {{"updateV", "val"}}},
		},
		"MultipleUpserts": {
			updates: bson.A{
				bson.D{
					{"q", bson.D{{"v", bson.D{{"$gt", 3}}}}},
					{"u", bson.D{{"updateV", "greater"}}},
					{"upsert", true},
				},
				bson.D{
					{"q", bson.D{{"v", bson.D{{"$lt", 3}}}}},
					{"u", bson.D{{"updateV", "less"}}},
					{"upsert", true},
				},
			},
			nMatched:  int32(2),
			nModified: int32(0),
			nUpserted: 2,
			findRes: []bson.D{
				{{"v", nil}},
				{{"updateV", "greater"}},
				{{"updateV", "less"}},
			},
		},
		"UnknownUpdateOperator": {
			updates: bson.A{
				bson.D{
					{"q", bson.D{{"v", bson.D{{"$lt", 3}}}}},
					{"u", bson.D{{"$unknown", bson.D{{"v", "val"}}}}},
					{"upsert", true},
				},
			},
			err: &mongo.WriteError{
				Code:    9,
				Message: "Unknown modifier: $unknown. Expected a valid update modifier or pipeline-style update specified as an array",
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NotNil(t, tc.updates, "update should be set")

			ctx, collection := setup.Setup(t, shareddata.Nulls)

			var res bson.D
			err := collection.Database().RunCommand(ctx,
				bson.D{
					{"update", collection.Name()},
					{"updates", tc.updates},
				}).Decode(&res)

			if tc.err != nil {
				assert.Nil(t, res)
				AssertEqualWriteError(t, *tc.err, err)

				return
			}

			doc := ConvertDocument(t, res)

			nMatched, _ := doc.Get("n")
			assert.Equal(t, tc.nMatched, nMatched, "unexpected nMatched")

			nModified, _ := doc.Get("nModified")
			assert.Equal(t, tc.nModified, nModified, "unexpected nModified")

			resOk, _ := doc.Get("ok")
			assert.Equal(t, float64(1), resOk)

			v, _ := doc.Get("upserted")
			upserted, _ := v.(*types.Array)
			require.Equal(t, tc.nUpserted, upserted.Len(), "unexpected nUpserted")

			for i := 0; i < upserted.Len(); i++ {
				firstElem, _ := must.NotFail(upserted.Get(i)).(*types.Document)

				index, _ := firstElem.Get("index")
				assert.Equal(t, int32(i), index, "unexpected index")

				// _id is generated, cannot check for exact value so check it is not zero value
				id, _ := firstElem.Get("_id")
				assert.NotZero(t, id)
			}

			cursor, err := collection.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{"v", 1}, {"_id", 1}}))
			require.NoError(t, err)

			var findRes []bson.D
			err = cursor.All(ctx, &findRes)
			require.NoError(t, err)

			for i, elem := range tc.findRes {
				doc := ConvertDocument(t, elem)

				actualDoc := ConvertDocument(t, findRes[i])
				_ = actualDoc.Remove("_id")

				require.Equal(t, doc, actualDoc)
			}
		})
	}
}

func TestUpdateFieldErrors(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // it is used for test only
		id       string              // optional, defaults to empty
		update   bson.D              // required, used for update parameter
		provider shareddata.Provider // optional, default uses shareddata.ArrayDocuments

		err        *mongo.WriteError // required, expected error from MongoDB
		altMessage string            // optional, alternative error message for FerretDB, ignored if empty
		skip       string            // optional, skip test with a specified reason
	}{
		"SetUnsuitableValue": {
			id:     "array-documents-nested",
			update: bson.D{{"$rename", bson.D{{"v.foo", "foo"}}}},
			err: &mongo.WriteError{
				Code: 28,
				Message: "cannot use the part (v of v.foo) to traverse the element " +
					"({v: [ { foo: [ { bar: \"hello\" }, { bar: \"world\" } ] } ]})",
			},
			altMessage: "cannot use path 'v.foo' to traverse the document",
		},
		"SetImmutableID": {
			id:     "array-documents-nested",
			update: bson.D{{"$set", bson.D{{"_id", "another-id"}}}},
			err: &mongo.WriteError{
				Code:    66,
				Message: "Performing an update on the path '_id' would modify the immutable field '_id'",
			},
			skip: "https://github.com/FerretDB/FerretDB/issues/3017",
		},
		"RenameEmptyFieldName": {
			id:     "array-documents-nested",
			update: bson.D{{"$rename", bson.D{{"", "v"}}}},
			err: &mongo.WriteError{
				Code:    56,
				Message: "An empty update path is not valid.",
			},
		},
		"RenameEmptyPath": {
			id:     "array-documents-nested",
			update: bson.D{{"$rename", bson.D{{"v.", "v"}}}},
			err: &mongo.WriteError{
				Code:    56,
				Message: "The update path 'v.' contains an empty field name, which is not allowed.",
			},
		},
		"RenameArrayInvalidIndex": {
			id:     "array-documents-nested",
			update: bson.D{{"$rename", bson.D{{"v.-1", "f"}}}},
			err: &mongo.WriteError{
				Code: 28,
				Message: "cannot use the part (v of v.-1) to traverse the element " +
					"({v: [ { foo: [ { bar: \"hello\" }, { bar: \"world\" } ] } ]})",
			},
			altMessage: "cannot use path 'v.-1' to traverse the document",
		},
		"RenameUnsuitableValue": {
			id:     "array-documents-nested",
			update: bson.D{{"$rename", bson.D{{"v.0.foo.0.bar.z", "f"}}}},
			err: &mongo.WriteError{
				Code:    28,
				Message: "cannot use the part (bar of v.0.foo.0.bar.z) to traverse the element ({bar: \"hello\"})",
			},
			altMessage: "types.getByPath: can't access string by path \"z\"",
		},
		"IncTypeMismatch": {
			id:     "array-documents-nested",
			update: bson.D{{"$inc", bson.D{{"v", "string"}}}},
			err: &mongo.WriteError{
				Code:    14,
				Message: "Cannot increment with non-numeric argument: {v: \"string\"}",
			},
		},
		"IncUnsuitableValue": {
			id:     "array-documents-nested",
			update: bson.D{{"$inc", bson.D{{"v.foo", 1}}}},
			err: &mongo.WriteError{
				Code: 28,
				Message: "Cannot create field 'foo' in element " +
					"{v: [ { foo: [ { bar: \"hello\" }, { bar: \"world\" } ] } ]}",
			},
		},
		"IncNonNumeric": {
			id:     "array-documents-nested",
			update: bson.D{{"$inc", bson.D{{"v.0.foo.0.bar", 1}}}},
			err: &mongo.WriteError{
				Code: 14,
				Message: "Cannot apply $inc to a value of non-numeric type. " +
					"{_id: \"array-documents-nested\"} has the field 'bar' of non-numeric type string",
			},
		},
		"IncInt64BadValue": {
			id:     "int64-max",
			update: bson.D{{"$inc", bson.D{{"v", math.MaxInt64}}}},
			err: &mongo.WriteError{
				Code: 2,
				Message: "Failed to apply $inc operations to current value " +
					"((NumberLong)9223372036854775807) for document {_id: \"int64-max\"}",
			},
			provider: shareddata.Int64s,
		},
		"IncInt32BadValue": {
			id:     "int32",
			update: bson.D{{"$inc", bson.D{{"v", math.MaxInt64}}}},
			err: &mongo.WriteError{
				Code: 2,
				Message: "Failed to apply $inc operations to current value " +
					"((NumberInt)42) for document {_id: \"int32\"}",
			},
			provider: shareddata.Int32s,
		},
		"MaxUnsuitableValue": {
			id:     "array-documents-nested",
			update: bson.D{{"$max", bson.D{{"v.foo", 1}}}},
			err: &mongo.WriteError{
				Code: 28,
				Message: "Cannot create field 'foo' in element " +
					"{v: [ { foo: [ { bar: \"hello\" }, { bar: \"world\" } ] } ]}",
			},
		},
		"MinUnsuitableValue": {
			id:     "array-documents-nested",
			update: bson.D{{"$min", bson.D{{"v.foo", 1}}}},
			err: &mongo.WriteError{
				Code: 28,
				Message: "Cannot create field 'foo' in element " +
					"{v: [ { foo: [ { bar: \"hello\" }, { bar: \"world\" } ] } ]}",
			},
		},
		"MulTypeMismatch": {
			id:     "array-documents-nested",
			update: bson.D{{"$mul", bson.D{{"v", "string"}}}},
			err: &mongo.WriteError{
				Code:    14,
				Message: "Cannot multiply with non-numeric argument: {v: \"string\"}",
			},
		},
		"MulTypeMismatchNonExistent": {
			id:     "array-documents-nested",
			update: bson.D{{"$mul", bson.D{{"non-existent", "string"}}}},
			err: &mongo.WriteError{
				Code:    14,
				Message: "Cannot multiply with non-numeric argument: {non-existent: \"string\"}",
			},
		},
		"MulUnsuitableValue": {
			id:     "array-documents-nested",
			update: bson.D{{"$mul", bson.D{{"v.foo", 1}}}},
			err: &mongo.WriteError{
				Code: 28,
				Message: "Cannot create field 'foo' in element " +
					"{v: [ { foo: [ { bar: \"hello\" }, { bar: \"world\" } ] } ]}",
			},
		},
		"MulNonNumeric": {
			id:     "array-documents-nested",
			update: bson.D{{"$mul", bson.D{{"v.0.foo.0.bar", 1}}}},
			err: &mongo.WriteError{
				Code: 14,
				Message: "Cannot apply $mul to a value of non-numeric type. " +
					"{_id: \"array-documents-nested\"} has the field 'bar' of non-numeric type string",
			},
		},
		"MulInt64BadValue": {
			id:     "int64-max",
			update: bson.D{{"$mul", bson.D{{"v", math.MaxInt64}}}},
			err: &mongo.WriteError{
				Code: 2,
				Message: "Failed to apply $mul operations to current value " +
					"((NumberLong)9223372036854775807) for document {_id: \"int64-max\"}",
			},
			provider: shareddata.Int64s,
		},
		"MulInt32BadValue": {
			id:     "int32",
			update: bson.D{{"$mul", bson.D{{"v", math.MaxInt64}}}},
			err: &mongo.WriteError{
				Code: 2,
				Message: "Failed to apply $mul operations to current value " +
					"((NumberInt)42) for document {_id: \"int32\"}",
			},
			provider: shareddata.Int32s,
		},
		"MulEmptyPath": {
			id:     "array-documents-nested",
			update: bson.D{{"$mul", bson.D{{"v.", "v"}}}},
			err: &mongo.WriteError{
				Code:    56,
				Message: "The update path 'v.' contains an empty field name, which is not allowed.",
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			t.Parallel()

			require.NotNil(t, tc.update, "update should be set")
			require.NotNil(t, tc.err, "err should be set")

			provider := tc.provider
			if provider == nil {
				provider = shareddata.ArrayDocuments
			}

			ctx, collection := setup.Setup(t, provider)

			res, err := collection.UpdateOne(ctx, bson.D{{"_id", tc.id}}, tc.update)

			assert.Nil(t, res)
			AssertEqualAltWriteError(t, *tc.err, tc.altMessage, err)
		})
	}
}
