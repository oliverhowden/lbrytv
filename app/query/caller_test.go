package query

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/lbryio/lbrytv/app/paid"
	"github.com/lbryio/lbrytv/app/sdkrouter"
	"github.com/lbryio/lbrytv/app/wallet"
	"github.com/lbryio/lbrytv/config"
	"github.com/lbryio/lbrytv/internal/test"

	ljsonrpc "github.com/lbryio/lbry.go/v2/extras/jsonrpc"

	"github.com/davecgh/go-spew/spew"
	logrusTest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ybbus/jsonrpc"
)

func parseRawResponse(t *testing.T, rawCallResponse []byte, v interface{}) {
	assert.NotNil(t, rawCallResponse)
	var res jsonrpc.RPCResponse
	err := json.Unmarshal(rawCallResponse, &res)
	require.NoError(t, err)
	err = res.GetObject(v)
	require.NoError(t, err)
}

func TestCaller_CallRelaxedMethods(t *testing.T) {
	reqChan := test.ReqChan()
	srv := test.MockHTTPServer(reqChan)
	defer srv.Close()
	caller := NewCaller(srv.URL, 0)

	for _, m := range relaxedMethods {
		if m == MethodStatus {
			continue
		}
		t.Run(m, func(t *testing.T) {
			srv.NextResponse <- test.EmptyResponse()
			resp := caller.Call(jsonrpc.NewRequest(m))
			require.NotEqual(t, "authentication required", test.StrToRes(t, string(resp)).Error.Message)

			receivedRequest := <-reqChan
			expectedRequest := test.ReqToStr(t, jsonrpc.RPCRequest{
				Method:  m,
				Params:  nil,
				JSONRPC: "2.0",
			})
			assert.EqualValues(t, expectedRequest, receivedRequest.Body)
		})
	}
}

func TestCaller_CallAmbivalentMethodsWithoutWallet(t *testing.T) {
	reqChan := test.ReqChan()
	srv := test.MockHTTPServer(reqChan)
	defer srv.Close()
	caller := NewCaller(srv.URL, 0)

	for _, m := range relaxedMethods {
		if !methodInList(m, walletSpecificMethods) {
			continue
		}
		t.Run(m, func(t *testing.T) {
			srv.NextResponse <- test.EmptyResponse()
			resp := caller.Call(jsonrpc.NewRequest(m))
			require.NotEqual(t, "authentication required", test.StrToRes(t, string(resp)).Error.Message)

			receivedRequest := <-reqChan
			expectedRequest := test.ReqToStr(t, jsonrpc.RPCRequest{
				Method:  m,
				Params:  nil,
				JSONRPC: "2.0",
			})
			assert.EqualValues(t, expectedRequest, receivedRequest.Body)
		})
	}
}

func TestCaller_CallAmbivalentMethodsWithWallet(t *testing.T) {
	reqChan := test.ReqChan()
	srv := test.MockHTTPServer(reqChan)
	defer srv.Close()
	dummyUserID := 123321
	authedCaller := NewCaller(srv.URL, dummyUserID)
	var methodsTested int

	for _, m := range relaxedMethods {
		if !methodInList(m, walletSpecificMethods) {
			continue
		}
		methodsTested++
		t.Run(m, func(t *testing.T) {
			srv.NextResponse <- test.EmptyResponse()
			resp := authedCaller.Call(jsonrpc.NewRequest(m))
			require.NotEqual(t, "authentication required", test.StrToRes(t, string(resp)).Error.Message)

			receivedRequest := <-reqChan
			expectedRequest := test.ReqToStr(t, jsonrpc.RPCRequest{
				Method: m,
				Params: map[string]interface{}{
					"wallet_id": sdkrouter.WalletID(dummyUserID),
				},
				JSONRPC: "2.0",
			})
			assert.EqualValues(t, expectedRequest, receivedRequest.Body)
		})
	}

	if methodsTested == 0 {
		t.Fatal("no ambivalent methods found, that doesn't look right")
	}
}

func TestCaller_CallNonRelaxedMethods(t *testing.T) {
	reqChan := test.ReqChan()
	srv := test.MockHTTPServer(reqChan)
	defer srv.Close()

	caller := NewCaller(srv.URL, 0)
	for _, m := range walletSpecificMethods {
		if methodInList(m, relaxedMethods) {
			continue
		}
		t.Run(m, func(t *testing.T) {
			resp := caller.Call(jsonrpc.NewRequest(m))
			select {
			case <-reqChan:
				t.Fatal("a request went throught")
			default:
			}
			assert.Equal(t, "authentication required", test.StrToRes(t, string(resp)).Error.Message)
		})
	}
}

func TestCaller_CallForbiddenMethod(t *testing.T) {
	caller := NewCaller(test.RandServerAddress(t), 0)
	result := caller.Call(jsonrpc.NewRequest("stop"))
	assert.Contains(t, string(result), `"message": "forbidden method"`)
}

func TestCaller_CallAttachesWalletID(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	dummyUserID := 123321

	reqChan := test.ReqChan()
	srv := test.MockHTTPServer(reqChan)
	defer srv.Close()
	srv.NextResponse <- test.EmptyResponse()
	caller := NewCaller(srv.URL, dummyUserID)
	caller.Call(jsonrpc.NewRequest("channel_create", map[string]interface{}{"name": "test", "bid": "0.1"}))
	receivedRequest := <-reqChan

	expectedRequest := test.ReqToStr(t, jsonrpc.RPCRequest{
		Method: "channel_create",
		Params: map[string]interface{}{
			"name":      "test",
			"bid":       "0.1",
			"wallet_id": sdkrouter.WalletID(dummyUserID),
		},
		JSONRPC: "2.0",
	})
	assert.EqualValues(t, expectedRequest, receivedRequest.Body)
}

func TestCaller_SetPreprocessor(t *testing.T) {
	reqChan := test.ReqChan()
	srv := test.MockHTTPServer(reqChan)
	defer srv.Close()

	c := NewCaller(srv.URL, 0)

	c.Preprocessor = func(q *Query) {
		params := q.ParamsAsMap()
		if params == nil {
			q.Request.Params = map[string]string{"param": "123"}
		} else {
			params["param"] = "123"
			q.Request.Params = params
		}
	}

	srv.NextResponse <- test.EmptyResponse()

	c.Call(jsonrpc.NewRequest(relaxedMethods[0]))
	req := <-reqChan
	lastRequest := test.StrToReq(t, req.Body)

	p, ok := lastRequest.Params.(map[string]interface{})
	assert.True(t, ok, req.Body)
	assert.Equal(t, "123", p["param"], req.Body)
}

func TestCaller_CallSDKError(t *testing.T) {
	srv := test.MockHTTPServer(nil)
	defer srv.Close()
	srv.NextResponse <- `
		{
			"jsonrpc": "2.0",
			"error": {
			  "code": -32500,
			  "message": "After successfully executing the command, failed to encode result for JSON RPC response.",
			  "data": [
				"Traceback (most recent call last):",
				"  File \"lbry/extras/daemon/Daemon.py\", line 482, in handle_old_jsonrpc",
				"  File \"lbry/extras/daemon/Daemon.py\", line 235, in jsonrpc_dumps_pretty",
				"  File \"json/__init__.py\", line 238, in dumps",
				"  File \"json/encoder.py\", line 201, in encode",
				"  File \"json/encoder.py\", line 431, in _iterencode",
				"  File \"json/encoder.py\", line 405, in _iterencode_dict",
				"  File \"json/encoder.py\", line 405, in _iterencode_dict",
				"  File \"json/encoder.py\", line 325, in _iterencode_list",
				"  File \"json/encoder.py\", line 438, in _iterencode",
				"  File \"lbry/extras/daemon/json_response_encoder.py\", line 118, in default",
				"  File \"lbry/extras/daemon/json_response_encoder.py\", line 151, in encode_output",
				"  File \"torba/client/baseheader.py\", line 75, in __getitem__",
				"  File \"lbry/wallet/header.py\", line 35, in deserialize",
				"struct.error: unpack requires a buffer of 4 bytes",
				""
			  ]
			},
			"id": 0
		}`

	c := NewCaller(srv.URL, 0)
	hook := logrusTest.NewLocal(logger.Entry.Logger)
	response := c.Call(jsonrpc.NewRequest("resolve", map[string]interface{}{"urls": "what"}))
	var rpcResponse jsonrpc.RPCResponse
	json.Unmarshal(response, &rpcResponse)
	assert.Equal(t, rpcResponse.Error.Code, -32500)
	assert.Equal(t, "query", hook.LastEntry().Data["module"])
	assert.Equal(t, "resolve", hook.LastEntry().Data["method"])
}

func TestCaller_ClientJSONError(t *testing.T) {
	ts := test.MockHTTPServer(nil)
	defer ts.Close()
	ts.NextResponse <- `{"method":"version}` // note the missing close quote after "version

	c := NewCaller(ts.URL, 0)
	response := c.CallRaw([]byte(""))
	spew.Dump(response)
	var rpcResponse jsonrpc.RPCResponse
	json.Unmarshal(response, &rpcResponse)
	assert.Equal(t, "2.0", rpcResponse.JSONRPC)
	assert.Equal(t, -32700, rpcResponse.Error.Code)
	assert.Equal(t, "unexpected end of JSON input", rpcResponse.Error.Message)
}

func TestCaller_CallRaw(t *testing.T) {
	c := NewCaller(test.RandServerAddress(t), 0)
	for _, rawQ := range []string{``, ` `, `[]`, `[{}]`, `[""]`, `""`, `" "`} {
		t.Run(rawQ, func(t *testing.T) {
			r := c.CallRaw([]byte(rawQ))
			assert.Contains(t, string(r), `"code": -32700`, `raw query: `+rawQ)
		})
	}
	for _, rawQ := range []string{`{}`, `{"method": " "}`} {
		t.Run(rawQ, func(t *testing.T) {
			r := c.CallRaw([]byte(rawQ))
			assert.Contains(t, string(r), `"code": -32080`, `raw query: `+rawQ)
		})
	}
}

func TestCaller_Resolve(t *testing.T) {
	resolvedURL := "what#6769855a9aa43b67086f9ff3c1a5bacb5698a27a"
	resolvedClaimID := "6769855a9aa43b67086f9ff3c1a5bacb5698a27a"

	request := jsonrpc.NewRequest("resolve", map[string]interface{}{"urls": resolvedURL})
	rawCallResponse := NewCaller(test.RandServerAddress(t), 0).Call(request)

	var errorResponse jsonrpc.RPCResponse
	err := json.Unmarshal(rawCallResponse, &errorResponse)
	require.NoError(t, err)
	require.Nil(t, errorResponse.Error)

	var resolveResponse ljsonrpc.ResolveResponse
	parseRawResponse(t, rawCallResponse, &resolveResponse)
	assert.Equal(t, resolvedClaimID, resolveResponse[resolvedURL].ClaimID)
}

func TestCaller_WalletBalance(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	dummyUserID := rand.Intn(10^6-10^3) + 10 ^ 3

	request := jsonrpc.NewRequest("wallet_balance")

	result := NewCaller(test.RandServerAddress(t), 0).Call(request)
	assert.Contains(t, string(result), `"message": "authentication required"`)

	addr := test.RandServerAddress(t)
	err := wallet.Create(addr, dummyUserID)
	require.NoError(t, err)

	hook := logrusTest.NewLocal(logger.Entry.Logger)
	result = NewCaller(addr, dummyUserID).Call(request)

	var accountBalanceResponse struct {
		Available string `json:"available"`
	}
	parseRawResponse(t, result, &accountBalanceResponse)
	assert.EqualValues(t, "0.0", accountBalanceResponse.Available)
	assert.Equal(t, map[string]interface{}{"wallet_id": sdkrouter.WalletID(dummyUserID)}, hook.LastEntry().Data["params"])
	assert.Equal(t, "wallet_balance", hook.LastEntry().Data["method"])
}

func TestCaller_CallQueryWithRetry(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	dummyUserID := rand.Intn(100)
	addr := test.RandServerAddress(t)

	err := wallet.Create(addr, dummyUserID)
	require.NoError(t, err)
	err = wallet.UnloadWallet(addr, dummyUserID)
	require.NoError(t, err)

	q, err := NewQuery(jsonrpc.NewRequest("wallet_balance"), sdkrouter.WalletID(dummyUserID))
	require.NoError(t, err)

	// check that sdk loads the wallet and retries the query if the wallet was not initially loaded

	c := NewCaller(addr, dummyUserID)
	r, err := c.callQueryWithRetry(q)
	require.NoError(t, err)
	require.Nil(t, r.Error)
}

func TestCaller_DontReloadWalletAfterOtherErrors(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	walletID := sdkrouter.WalletID(rand.Intn(100))

	srv := test.MockHTTPServer(nil)
	defer srv.Close()

	c := NewCaller(srv.URL, 0)
	q, err := NewQuery(jsonrpc.NewRequest("wallet_balance"), walletID)
	require.NoError(t, err)

	srv.QueueResponses(
		test.ResToStr(t, jsonrpc.RPCResponse{
			JSONRPC: "2.0",
			Error: &jsonrpc.RPCError{
				Message: "Couldn't find wallet: //",
			},
		}),
		test.EmptyResponse(), // for the wallet_add call
		test.ResToStr(t, jsonrpc.RPCResponse{
			JSONRPC: "2.0",
			Error: &jsonrpc.RPCError{
				Message: "Wallet at path // was not found",
			},
		}),
	)

	r, err := c.callQueryWithRetry(q)
	require.NoError(t, err)
	require.Equal(t, "Wallet at path // was not found", r.Error.Message)
}

func TestCaller_DontReloadWalletIfAlreadyLoaded(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	walletID := sdkrouter.WalletID(rand.Intn(100))

	srv := test.MockHTTPServer(nil)
	defer srv.Close()

	c := NewCaller(srv.URL, 0)
	q, err := NewQuery(jsonrpc.NewRequest("wallet_balance"), walletID)
	require.NoError(t, err)

	srv.QueueResponses(
		test.ResToStr(t, jsonrpc.RPCResponse{
			JSONRPC: "2.0",
			Error: &jsonrpc.RPCError{
				Message: "Couldn't find wallet: //",
			},
		}),
		test.EmptyResponse(), // for the wallet_add call
		test.ResToStr(t, jsonrpc.RPCResponse{
			JSONRPC: "2.0",
			Error: &jsonrpc.RPCError{
				Message: "Wallet at path // is already loaded",
			},
		}),
		test.ResToStr(t, jsonrpc.RPCResponse{
			JSONRPC: "2.0",
			Result:  `"99999.00"`,
		}),
	)

	r, err := c.callQueryWithRetry(q)

	require.NoError(t, err)
	require.Nil(t, r.Error)
	require.Equal(t, `"99999.00"`, r.Result)
}

func TestSDKMethodStatus(t *testing.T) {
	c := NewCaller(test.RandServerAddress(t), 0)
	callResult := c.Call(jsonrpc.NewRequest("status"))
	var rpcResponse jsonrpc.RPCResponse
	json.Unmarshal(callResult, &rpcResponse)
	assert.Equal(t,
		"692EAWhtoqDuAfQ6KHMXxFxt8tkhmt7sfprEMHWKjy5hf6PwZcHDV542VHqRnFnTCD",
		rpcResponse.Result.(map[string]interface{})["installation_id"].(string))
}

func TestCaller_GetPaidCannotPurchase(t *testing.T) {
	dummyUserID := rand.Intn(99999)
	srvAddress := test.RandServerAddress(t)
	uri := "lbry://@specialoperationstest#3/iOS-13-AdobeXD#9"
	err := wallet.Create(srvAddress, dummyUserID)

	request := jsonrpc.NewRequest(MethodGet, map[string]interface{}{"uri": uri})
	rawCallResponse := NewCaller(srvAddress, dummyUserID).Call(request)

	var errorResponse jsonrpc.RPCResponse
	err = json.Unmarshal(rawCallResponse, &errorResponse)
	require.NoError(t, err)
	fmt.Println(string(rawCallResponse))
	assert.Equal(t, "Not enough funds to cover this transaction.", errorResponse.Result.(map[string]interface{})["error"])
}

func TestCaller_GetPaidPurchased(t *testing.T) {
	config.Override("BaseContentURL", "https://cdn.lbryplayer.xyz/api/v2/streams/")
	defer config.RestoreOverridden()

	uri := "lbry://@specialoperationstest#3/iOS-13-AdobeXD#9"
	txid := "ff990688df370072f408e2db9d217d2cf331d92ac594d5e6e8391143e9d38160"
	sdHash := "51ee258ebbe33c15d37a28e90b1ba1e9ddfddd277bede52bd59431ce1b6ed6475f6c2c7299210a98eb3b746cbffa1f94"

	dummyUserID := 123321
	srv := test.MockHTTPServer(nil)

	srv.NextResponse <- `
{
  "id": 0,
  "jsonrpc": "2.0",
  "result": {
    "added_on": 1588072810,
    "blobs_completed": 280,
    "blobs_in_stream": 280,
    "blobs_remaining": 0,
    "channel_claim_id": "f399d873e0c37cf24de9569b5f22bbb30a5c6709",
    "channel_name": "@Bombards_Body_Language",
    "claim_id": "d66f8ba85c85ca48daba9183bd349307fe30cb43",
    "claim_name": "Body-Language---Robert-F.-Kennedy-Assassination---Hypnosis",
    "completed": true,
    "confirmations": 12167,
    "content_fee": {
      "height": -2,
      "hex": "0100000001d1adb1a25a7737bb8e9382b59cbf214aedbc29eae79a42b8a3fc720f1e8576ec000000006b483045022100aef35f0abf575b17cb854e4998d70df93e3606cc9ccec72967957a6325e14200022059dd6c083397d1889254c6414f23d34fea2f7b2922c0d396f1a5072a2129c44701210352f24fc35047e17a00bd1d5786c15503f8f0c1a67be143f6bc66de741a26f02dffffffff0300ba1dd2050000001976a914c4425439537bf7f8c0c1dca66490826e90dfffde88ac0000000000000000196a17500a1443cb30fe079334bd8391bada48ca855ca88b6fd6ecba052a010000001976a9140b934e8f1b7e53023265108371bfbffec2ffa91e88ac00000000",
      "inputs": [
        {
          "nout": 0,
          "txid": "ec76851e0f72fca3b8429ae7ea29bced4a21bf9cb582938ebb37775aa2b1add1"
        }
      ],
      "outputs": [
        {
          "address": "bWczbT1P6JQQ63PiDvFiYbkRYpQs6h6oap",
          "amount": "250.0",
          "confirmations": -2,
          "height": -2,
          "nout": 0,
          "timestamp": null,
          "txid": "ff990688df370072f408e2db9d217d2cf331d92ac594d5e6e8391143e9d38160",
          "type": "payment"
        },
        {
          "address": null,
          "amount": "0.0",
          "confirmations": -2,
          "height": -2,
          "nout": 1,
          "timestamp": null,
          "txid": "ff990688df370072f408e2db9d217d2cf331d92ac594d5e6e8391143e9d38160",
          "type": "data"
        },
        {
          "address": "bDnUbQFeXMaza5wUFjG8TJ3MXiGDYW88kF",
          "amount": "49.999859",
          "confirmations": -2,
          "height": -2,
          "nout": 2,
          "timestamp": null,
          "txid": "ff990688df370072f408e2db9d217d2cf331d92ac594d5e6e8391143e9d38160",
          "type": "payment"
        }
      ],
      "total_fee": "-299.999859",
      "total_input": "0.0",
      "total_output": "299.999859",
      "txid": "ff990688df370072f408e2db9d217d2cf331d92ac594d5e6e8391143e9d38160"
    },
    "download_directory": "...",
    "download_path": "...",
    "file_name": "Body Language - Robert F. Kennedy Assassination & Hypnosis.mp4",
    "height": 752080,
    "is_fully_reflected": true,
    "key": "467528ee803c66af14fc3a6b7f583305",
    "metadata": {
      "description": "This is one of my personal favourites! \n\nTo help support this channel and to learn more about body language, You can visit my website where you can view exclusive content, as well as a tutorial series that explains my methods in more detail.\n\nhttps://bombardsbodylanguage.com/\n\nNote: All comments in my videos are strictly my opinion.",
      "fee": {
        "address": "bWczbT1P6JQQ63PiDvFiYbkRYpQs6h6oap",
        "amount": "250",
        "currency": "LBC"
      },
      "languages": [
        "en"
      ],
      "license": "None",
      "release_time": "1587499210",
      "source": {
        "hash": "fae1e6db07c03a857f526ae9956d80be64dd95b85eeb79560d5f0fb8aea6e70531f089587f946f8916f42052abdb4fb2",
        "media_type": "video/mp4",
        "name": "Body Language - Robert F. Kennedy Assassination & Hypnosis.mp4",
        "sd_hash": "51ee258ebbe33c15d37a28e90b1ba1e9ddfddd277bede52bd59431ce1b6ed6475f6c2c7299210a98eb3b746cbffa1f94",
        "size": "585600621"
      },
      "stream_type": "video",
      "tags": [
        "assassination",
        "body language",
        "education",
        "hypnosis",
        "kennedy"
      ],
      "thumbnail": {
        "url": "https://spee.ch/0/EVTMYSEf0OLuvjkMGRrFHubl.jpeg"
      },
      "title": "Body Language - Robert F. Kennedy Assassination & Hypnosis",
      "video": {
        "duration": 1504,
        "height": 1080,
        "width": 1920
      }
    },
    "mime_type": "video/mp4",
    "nout": 0,
    "outpoint": "a6005c8b55122eb1663041362546928e5961a037882fa04d52e70c190324ee64:0",
    "points_paid": 0,
    "protobuf": "0109675c0ab3bb225f9b56e94df27cc3e073d899f3e1cc696925f6f820375292404447f6c8b61214df0444994f0458042ea95a37b531ebcd6b3dd6092914c78270a197b07909382031efcf7d1c32c7d8c27ac526740af4010ab5010a30fae1e6db07c03a857f526ae9956d80be64dd95b85eeb79560d5f0fb8aea6e70531f089587f946f8916f42052abdb4fb2123e426f6479204c616e6775616765202d20526f6265727420462e204b656e6e65647920417373617373696e6174696f6e2026204879706e6f7369732e6d703418ed9c9e97022209766964656f2f6d7034323051ee258ebbe33c15d37a28e90b1ba1e9ddfddd277bede52bd59431ce1b6ed6475f6c2c7299210a98eb3b746cbffa1f941a044e6f6e6528caa1fdf40532230801121955c4425439537bf7f8c0c1dca66490826e90dfffdeaa6b54891880f4f6905d5a0908800f10b80818e00b423a426f6479204c616e6775616765202d20526f6265727420462e204b656e6e65647920417373617373696e6174696f6e2026204879706e6f7369734ace0254686973206973206f6e65206f66206d7920706572736f6e616c206661766f75726974657321200a0a546f2068656c7020737570706f72742074686973206368616e6e656c20616e6420746f206c6561726e206d6f72652061626f757420626f6479206c616e67756167652c20596f752063616e207669736974206d79207765627369746520776865726520796f752063616e2076696577206578636c757369766520636f6e74656e742c2061732077656c6c2061732061207475746f7269616c207365726965732074686174206578706c61696e73206d79206d6574686f647320696e206d6f72652064657461696c2e0a0a68747470733a2f2f626f6d6261726473626f64796c616e67756167652e636f6d2f0a0a4e6f74653a20416c6c20636f6d6d656e747320696e206d7920766964656f7320617265207374726963746c79206d79206f70696e696f6e2e52312a2f68747470733a2f2f737065652e63682f302f4556544d59534566304f4c75766a6b4d475272464875626c2e6a7065675a0d617373617373696e6174696f6e5a0d626f6479206c616e67756167655a09656475636174696f6e5a086879706e6f7369735a076b656e6e65647962020801",
    "purchase_receipt": {
      "address": "bWczbT1P6JQQ63PiDvFiYbkRYpQs6h6oap",
      "amount": "250.0",
      "claim_id": "d66f8ba85c85ca48daba9183bd349307fe30cb43",
      "confirmations": 8630,
      "height": 755617,
      "nout": 0,
      "timestamp": 1588072854,
      "txid": "ff990688df370072f408e2db9d217d2cf331d92ac594d5e6e8391143e9d38160",
      "type": "purchase"
    },
    "reflector_progress": 0,
    "sd_hash": "51ee258ebbe33c15d37a28e90b1ba1e9ddfddd277bede52bd59431ce1b6ed6475f6c2c7299210a98eb3b746cbffa1f94",
    "status": "running",
    "stopped": false,
    "stream_hash": "a34634bfdb5cc1722a97653ce48916eafce5925fa771786a05f4c0f5eeb7d4761e575d6b59894cfdadba98211ce25031",
    "stream_name": "Body Language - Robert F. Kennedy Assassination & Hypnosis.mp4",
    "streaming_url": "http://localhost:5280/stream/51ee258ebbe33c15d37a28e90b1ba1e9ddfddd277bede52bd59431ce1b6ed6475f6c2c7299210a98eb3b746cbffa1f94",
    "suggested_file_name": "Body Language - Robert F. Kennedy Assassination & Hypnosis.mp4",
    "timestamp": 1587500654,
    "total_bytes": 585600633,
    "total_bytes_lower_bound": 585600617,
    "txid": "a6005c8b55122eb1663041362546928e5961a037882fa04d52e70c190324ee64",
    "uploading_to_reflector": false,
    "written_bytes": 585600621
  }
}
	`

	err := paid.GeneratePrivateKey()
	require.NoError(t, err)

	token, err := paid.CreateToken(sdHash, txid, 585600621, paid.ExpTenSecPer100MB)
	require.NoError(t, err)

	request := jsonrpc.NewRequest(MethodGet, map[string]interface{}{"uri": uri})
	rawCallResponse := NewCaller(srv.URL, dummyUserID).Call(request)

	var errorResponse jsonrpc.RPCResponse
	err = json.Unmarshal(rawCallResponse, &errorResponse)
	require.NoError(t, err)
	require.Nil(t, errorResponse.Error)

	var getResponse ljsonrpc.GetResponse
	parseRawResponse(t, rawCallResponse, &getResponse)
	assert.Equal(t, "https://cdn.lbryplayer.xyz/api/v2/streams/paid/"+sdHash+"/"+token, getResponse.StreamingURL)
}

func TestCaller_GetFree(t *testing.T) {
	config.Override("BaseContentURL", "https://cdn.lbryplayer.xyz/api/v2/streams/")
	defer config.RestoreOverridden()

	uri := "lbry://what"

	dummyUserID := 123321
	srv := test.MockHTTPServer(nil)

	srv.NextResponse <- `
	{
		"id": 0,
		"jsonrpc": "2.0",
		"result":
		{
			"added_on": 1589469363,
			"blobs_completed": 0,
			"blobs_in_stream": 76,
			"blobs_remaining": 76,
			"channel_claim_id": null,
			"channel_name": null,
			"claim_id": "19b9c243bea0c45175e6a6027911abbad53e983e",
			"claim_name": "what",
			"completed": false,
			"confirmations": 377229,
			"content_fee": null,
			"download_directory": null,
			"download_path": null,
			"file_name": null,
			"height": 387055,
			"is_fully_reflected": false,
			"key": "0edc1705489d7a2b2bcad3fea7e5ce92",
			"metadata": {
			  "author": "Samuel Bryan",
			  "description": "What is LBRY? An introduction with Alex Tabarrok",
			  "languages": [
				"en"
			  ],
			  "license": "LBRY inc",
			  "source": {
				"media_type": "video/mp4",
				"sd_hash": "d5169241150022f996fa7cd6a9a1c421937276a3275eb912790bd07ba7aec1fac5fd45431d226b8fb402691e79aeb24b"
			  },
			  "stream_type": "video",
			  "thumbnail": {
				"url": "https://s3.amazonaws.com/files.lbry.io/logo.png"
			  },
			  "title": "What is LBRY?"
			},
			"mime_type": "video/mp4",
			"nout": 0,
			"outpoint": "555ef2de37698f1c1d36d1d95bdf7b18d51483c76765a2ca63ff45bae5df65e9:0",
			"points_paid": 0.0,
			"protobuf": "000a570a3d2209766964656f2f6d70343230d5169241150022f996fa7cd6a9a1c421937276a3275eb912790bd07ba7aec1fac5fd45431d226b8fb402691e79aeb24b120c53616d75656c20427279616e1a084c42525920696e63420d57686174206973204c4252593f4a3057686174206973204c4252593f20416e20696e74726f64756374696f6e207769746820416c6578205461626172726f6b52312a2f68747470733a2f2f73332e616d617a6f6e6177732e636f6d2f66696c65732e6c6272792e696f2f6c6f676f2e706e6762020801",
			"purchase_receipt": null,
			"reflector_progress": 0,
			"sd_hash": "d5169241150022f996fa7cd6a9a1c421937276a3275eb912790bd07ba7aec1fac5fd45431d226b8fb402691e79aeb24b",
			"status": "running",
			"stopped": false,
			"stream_hash": "9f41e37b1ea706d1b431a65f634b89c5aadefb106280da3661e4d565d47bc938a345755cafb2af807bcfc9fbde3306e3",
			"stream_name": "LBRY100.mp4",
			"streaming_url": "http://localhost:5280/stream/d5169241150022f996fa7cd6a9a1c421937276a3275eb912790bd07ba7aec1fac5fd45431d226b8fb402691e79aeb24b",
			"suggested_file_name": "LBRY100.mp4",
			"timestamp": 1529028062,
			"total_bytes": 158433829,
			"total_bytes_lower_bound": 158433813,
			"txid": "555ef2de37698f1c1d36d1d95bdf7b18d51483c76765a2ca63ff45bae5df65e9",
			"uploading_to_reflector": false,
			"written_bytes": 0
		  }
	}
	`

	request := jsonrpc.NewRequest(MethodGet, map[string]interface{}{"uri": uri})
	rawCallResponse := NewCaller(srv.URL, dummyUserID).Call(request)

	var errorResponse jsonrpc.RPCResponse
	err := json.Unmarshal(rawCallResponse, &errorResponse)
	require.NoError(t, err)
	require.Nil(t, errorResponse.Error)

	var getResponse ljsonrpc.GetResponse
	parseRawResponse(t, rawCallResponse, &getResponse)
	assert.Equal(t, "https://cdn.lbryplayer.xyz/api/v2/streams/free/d5169241150022f996fa7cd6a9a1c421937276a3275eb912790bd07ba7aec1fac5fd45431d226b8fb402691e79aeb24b", getResponse.StreamingURL)
}