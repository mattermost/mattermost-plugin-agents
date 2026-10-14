// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/mock"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// testArtifactNonce is a well-formed host nonce (22 base64url characters).
const testArtifactNonce = "AAECAwQFBgcICQoLDA0ODw"

func TestHandleGetArtifact(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	sessionID := model.NewId()
	fileID := model.NewId()
	postID := model.NewId()
	channelID := model.NewId()
	const htmlDoc = "<html><head><title>x</title></head><body><script>run()</script></body></html>"

	htmlInfo := func() *model.FileInfo {
		return &model.FileInfo{Id: fileID, PostId: postID, ChannelId: channelID, Name: "chart.html", Extension: "html", Size: int64(len(htmlDoc))}
	}
	botPost := func() *model.Post {
		return &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}
	}
	// authorized sets up every lookup a successful authorization makes,
	// plus the config read used for the size limit.
	authorized := func(m *mocks.MockClient, info *model.FileInfo, cfg *model.Config) {
		m.EXPECT().GetFileInfo(fileID).Return(info, nil)
		m.EXPECT().GetPost(postID).Return(botPost(), nil)
		m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
		m.EXPECT().GetConfig().Return(cfg)
	}

	tests := []struct {
		name     string
		disabled bool
		fileID   string
		setup    func(m *mocks.MockClient)
		// serveSetup adds expectations only the artifact route reaches;
		// the token route stops after authorization.
		serveSetup func(m *mocks.MockClient)
		wantStatus int
		// wantTokenStatus is the token route's status when it differs.
		wantTokenStatus int
		// wantTooLarge is the token route's tooLarge flag on success.
		wantTooLarge bool
	}{
		{name: "disabled returns 404", disabled: true, fileID: fileID, wantStatus: http.StatusNotFound},
		{name: "invalid file id returns 400", fileID: "bad", wantStatus: http.StatusBadRequest},
		{
			name:   "file info not found returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(nil, &model.AppError{Message: "nope"})
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "non-html file returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				info := htmlInfo()
				info.Name, info.Extension = "notes.txt", "txt"
				m.EXPECT().GetFileInfo(fileID).Return(info, nil)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "file without post returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				info := htmlInfo()
				info.PostId = ""
				m.EXPECT().GetFileInfo(fileID).Return(info, nil)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "post not found returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				m.EXPECT().GetPost(postID).Return(nil, &model.AppError{Message: "nope"})
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "file not attached to post returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				p := botPost()
				p.FileIds = model.StringArray{model.NewId()}
				m.EXPECT().GetPost(postID).Return(p, nil)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "post by non-bot user returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				p := botPost()
				p.UserId = model.NewId()
				m.EXPECT().GetPost(postID).Return(p, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "non-bot post in unreadable channel returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				p := botPost()
				p.UserId = model.NewId()
				m.EXPECT().GetPost(postID).Return(p, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(false)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "viewer without channel permission returns 404",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(htmlInfo(), nil)
				m.EXPECT().GetPost(postID).Return(botPost(), nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(false)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:   "file over file size limit returns 413",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				info := htmlInfo()
				info.Size = 11
				authorized(m, info, &model.Config{FileSettings: model.FileSettings{MaxFileSize: model.NewPointer(int64(10))}})
			},
			wantStatus:      http.StatusRequestEntityTooLarge,
			wantTokenStatus: http.StatusOK,
			wantTooLarge:    true,
		},
		{
			name:   "file over render cap returns 413",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				info := htmlInfo()
				info.Size = maxArtifactRenderBytes + 1
				authorized(m, info, &model.Config{FileSettings: model.FileSettings{MaxFileSize: model.NewPointer(int64(100 * 1024 * 1024))}})
			},
			wantStatus:      http.StatusRequestEntityTooLarge,
			wantTokenStatus: http.StatusOK,
			wantTooLarge:    true,
		},
		{
			name:   "content longer than reported size is cut off with 413",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				authorized(m, htmlInfo(), &model.Config{FileSettings: model.FileSettings{MaxFileSize: model.NewPointer(int64(len(htmlDoc)))}})
			},
			serveSetup: func(m *mocks.MockClient) {
				m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(htmlDoc+"<p>more</p>")), nil)
			},
			wantStatus:      http.StatusRequestEntityTooLarge,
			wantTokenStatus: http.StatusOK,
		},
		{
			name:   "success serves injected html",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				authorized(m, htmlInfo(), &model.Config{})
			},
			serveSetup: func(m *mocks.MockClient) {
				m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(htmlDoc)), nil)
			},
			wantStatus: http.StatusOK,
		},
	}

	secret := []byte(strings.Repeat("k", artifactSecretSize))
	wantToken := artifactBridgeToken(secret, testUserID, fileID, testArtifactNonce)

	for _, route := range []string{"artifact", "token"} {
		for _, tt := range tests {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)
				e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
				e.config.enableHTMLArtifacts = !tt.disabled
				e.api.artifactSecretCache = secret

				m := mocks.NewMockClient(t)
				m.EXPECT().HasPermissionToFileAction(sessionID, tt.fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true).Maybe()
				if tt.setup != nil {
					tt.setup(m)
				}
				wantStatus := tt.wantStatus
				path := "/artifacts/" + tt.fileID
				if route == "token" {
					path += "/token"
					if tt.wantTokenStatus != 0 {
						wantStatus = tt.wantTokenStatus
					}
				} else if tt.serveSetup != nil {
					tt.serveSetup(m)
				}
				e.api.mmClient = m

				req := httptest.NewRequest(http.MethodGet, path+"?n="+testArtifactNonce, nil)
				req.Header.Set("Mattermost-User-Id", testUserID)
				req.Header.Set("Sec-Fetch-Dest", "iframe")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				rec := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)

				require.Equal(t, wantStatus, rec.Code)
				assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
				assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
				if wantStatus != http.StatusOK {
					assert.NotContains(t, rec.Body.String(), "run()")
					assert.NotContains(t, rec.Body.String(), wantToken)
					return
				}
				if route == "token" {
					var got struct {
						Token    string `json:"token"`
						TooLarge bool   `json:"tooLarge"`
					}
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
					assert.Equal(t, wantToken, got.Token)
					assert.Equal(t, tt.wantTooLarge, got.TooLarge)
					return
				}
				assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
				assert.Equal(t, artifactCSP, rec.Header().Get("Content-Security-Policy"))
				assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox allow-scripts;")
				assert.NotContains(t, rec.Header().Get("Content-Security-Policy"), "allow-same-origin")
				assert.Equal(t, "()", rec.Header().Get("Connection-Allowlist"))
				assert.Equal(t, artifactPermissionsPolicy, rec.Header().Get("Permissions-Policy"))
				assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
				assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Resource-Policy"))
				assert.Equal(t, "inline", rec.Header().Get("Content-Disposition"))
				assert.Equal(t, string(injectArtifactBridge([]byte(htmlDoc), wantToken, "*")), rec.Body.String())
				assert.Contains(t, rec.Body.String(), "const TOKEN = \""+wantToken+"\";")
			})
		}
	}
}

func TestHandleGetArtifactTopLevel(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	sessionID := model.NewId()
	fileID := model.NewId()
	postID := model.NewId()
	channelID := model.NewId()
	const htmlDoc = "<html><body><script>run()</script></body></html>"
	secret := []byte(strings.Repeat("k", artifactSecretSize))
	wantToken := artifactBridgeToken(secret, testUserID, fileID, testArtifactNonce)

	tests := []struct {
		name      string
		dest      string
		site      string
		query     string
		wantServe bool
	}{
		{name: "missing headers show interstitial", query: "n=" + testArtifactNonce},
		{name: "document shows interstitial", dest: "document", site: "same-origin", query: "n=" + testArtifactNonce},
		{name: "empty shows interstitial", dest: "empty", site: "same-origin", query: "n=" + testArtifactNonce},
		{name: "iframe without site shows interstitial", dest: "iframe", query: "n=" + testArtifactNonce},
		{name: "cross-site iframe shows interstitial", dest: "iframe", site: "cross-site", query: "n=" + testArtifactNonce},
		{name: "same-site iframe shows interstitial", dest: "iframe", site: "same-site", query: "n=" + testArtifactNonce},
		{name: "user-initiated iframe shows interstitial", dest: "iframe", site: "none", query: "n=" + testArtifactNonce},
		{name: "top-level visit without nonce shows interstitial", dest: "document", site: "none"},
		{name: "cross-site iframe with malformed nonce shows interstitial", dest: "iframe", site: "cross-site", query: "n=x&n=y"},
		{name: "same-origin iframe serves artifact", dest: "iframe", site: "same-origin", query: "n=" + testArtifactNonce, wantServe: true},
		{name: "headers are case-insensitive", dest: "IFrame", site: "Same-Origin", query: "n=" + testArtifactNonce, wantServe: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			e.config.enableHTMLArtifacts = true
			e.api.artifactSecretCache = secret

			m := mocks.NewMockClient(t)
			m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true).Maybe()
			m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, PostId: postID, ChannelId: channelID, Name: "a.html", Extension: "html", Size: int64(len(htmlDoc))}, nil)
			m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
			m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
			if tt.wantServe {
				m.EXPECT().GetConfig().Return(&model.Config{})
				m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(htmlDoc)), nil)
			}
			e.api.mmClient = m

			req := httptest.NewRequest(http.MethodGet, "/artifacts/"+fileID+"?"+tt.query, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			if tt.dest != "" {
				req.Header.Set("Sec-Fetch-Dest", tt.dest)
			}
			if tt.site != "" {
				req.Header.Set("Sec-Fetch-Site", tt.site)
			}
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
			assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
			if tt.wantServe {
				assert.Equal(t, artifactCSP, rec.Header().Get("Content-Security-Policy"))
				assert.Contains(t, rec.Body.String(), "run()")
				return
			}
			assert.Equal(t, artifactInterstitialCSP, rec.Header().Get("Content-Security-Policy"))
			assert.Contains(t, rec.Body.String(), "can only be viewed inside Mattermost")
			assert.NotContains(t, rec.Body.String(), "run()")
			assert.NotContains(t, rec.Body.String(), wantToken)
			assert.NotContains(t, strings.ToLower(rec.Body.String()), "<script")
		})
	}
}

func TestHandleGetArtifactNonceValidation(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	sessionID := model.NewId()
	fileID := model.NewId()
	postID := model.NewId()
	channelID := model.NewId()
	const htmlDoc = "<html><body><script>run()</script></body></html>"
	secret := []byte(strings.Repeat("k", artifactSecretSize))
	otherNonce := "zyxwvutsrqponmlkjihg-_"

	tests := []struct {
		name  string
		query string
		// unauthorized makes the viewer unable to read the channel.
		unauthorized bool
		wantStatus   int
		// wantNonce is the nonce the served token must be bound to.
		wantNonce string
	}{
		{name: "valid nonce", query: "n=" + testArtifactNonce, wantStatus: http.StatusOK, wantNonce: testArtifactNonce},
		{name: "token is bound to the given nonce", query: "n=" + otherNonce, wantStatus: http.StatusOK, wantNonce: otherNonce},
		{name: "missing query", query: "", wantStatus: http.StatusBadRequest},
		{name: "empty nonce", query: "n=", wantStatus: http.StatusBadRequest},
		{name: "key without value", query: "n", wantStatus: http.StatusBadRequest},
		{name: "nonce too short", query: "n=" + testArtifactNonce[1:], wantStatus: http.StatusBadRequest},
		{name: "nonce too long", query: "n=" + testArtifactNonce + "A", wantStatus: http.StatusBadRequest},
		{name: "padded nonce", query: "n=" + testArtifactNonce[2:] + "%3D%3D", wantStatus: http.StatusBadRequest},
		{name: "non-base64url character", query: "n=" + testArtifactNonce[1:] + ".", wantStatus: http.StatusBadRequest},
		{name: "standard base64 character", query: "n=" + testArtifactNonce[1:] + "%2B", wantStatus: http.StatusBadRequest},
		{name: "repeated nonce", query: "n=" + testArtifactNonce + "&n=" + testArtifactNonce, wantStatus: http.StatusBadRequest},
		{name: "extra parameter", query: "n=" + testArtifactNonce + "&x=1", wantStatus: http.StatusBadRequest},
		{name: "wrong parameter name", query: "nonce=" + testArtifactNonce, wantStatus: http.StatusBadRequest},
		{name: "unparsable query", query: "n=" + testArtifactNonce + ";x=1", wantStatus: http.StatusBadRequest},
		{name: "unauthorized viewer with invalid query gets 404", query: "n=x&n=y", unauthorized: true, wantStatus: http.StatusNotFound},
		{name: "unauthorized viewer with valid nonce gets 404", query: "n=" + testArtifactNonce, unauthorized: true, wantStatus: http.StatusNotFound},
	}

	for _, route := range []string{"artifact", "token"} {
		for _, tt := range tests {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)
				e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
				e.config.enableHTMLArtifacts = true
				e.api.artifactSecretCache = secret

				m := mocks.NewMockClient(t)
				m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true).Maybe()
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, PostId: postID, ChannelId: channelID, Name: "a.html", Extension: "html", Size: int64(len(htmlDoc))}, nil)
				m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
				m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(!tt.unauthorized)
				m.EXPECT().GetConfig().Return(&model.Config{}).Maybe()
				m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(htmlDoc)), nil).Maybe()
				e.api.mmClient = m

				path := "/artifacts/" + fileID
				if route == "token" {
					path += "/token"
				}
				if tt.query != "" {
					path += "?" + tt.query
				}
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Mattermost-User-Id", testUserID)
				req.Header.Set("Sec-Fetch-Dest", "iframe")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				rec := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)

				require.Equal(t, tt.wantStatus, rec.Code)
				switch tt.wantStatus {
				case http.StatusBadRequest:
					assert.Equal(t, "invalid request", rec.Body.String())
					assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain"))
					return
				case http.StatusNotFound:
					assert.Equal(t, "not found", rec.Body.String())
					return
				}

				wantToken := artifactBridgeToken(secret, testUserID, fileID, tt.wantNonce)
				if route == "token" {
					var got struct {
						Token string `json:"token"`
					}
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
					assert.Equal(t, wantToken, got.Token)
					return
				}
				assert.Contains(t, rec.Body.String(), "const TOKEN = \""+wantToken+"\";")
				assert.Contains(t, rec.Body.String(), "run()")
			})
		}
	}
}

func TestInjectArtifactBridge(t *testing.T) {
	bridge := artifactBridgeScript("tok", "*")

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "doctype then html and head",
			doc:  "<!doctype html><html><head><script>a()</script></head><body></body></html>",
			want: "<!doctype html>" + bridge + "<html><head><script>a()</script></head><body></body></html>",
		},
		{
			name: "no doctype",
			doc:  `<html lang="en"><head data-x="1"><script>a()</script></head></html>`,
			want: bridge + `<html lang="en"><head data-x="1"><script>a()</script></head></html>`,
		},
		{
			name: "uppercase doctype",
			doc:  "<!DOCTYPE HTML><HTML><HEAD><SCRIPT>a()</SCRIPT></HEAD></HTML>",
			want: "<!DOCTYPE HTML>" + bridge + "<HTML><HEAD><SCRIPT>a()</SCRIPT></HEAD></HTML>",
		},
		{
			name: "bom whitespace and doctype",
			doc:  "\xEF\xBB\xBF \n<!DOCTYPE html>\n<html>\n<head><script>a()</script></head></html>",
			want: "\xEF\xBB\xBF \n<!DOCTYPE html>\n" + bridge + "<html>\n<head><script>a()</script></head></html>",
		},
		{
			name: "comment containing head before doctype",
			doc:  "<!-- <head> --><!doctype html><html><head><script>a()</script></head></html>",
			want: "<!-- <head> --><!doctype html>" + bridge + "<html><head><script>a()</script></head></html>",
		},
		{
			name: "unquoted attribute value hiding a script",
			doc:  `<html a=x="><script>a()</script>" ><head></head></html>`,
			want: bridge + `<html a=x="><script>a()</script>" ><head></head></html>`,
		},
		{
			name: "quoted gt in head attribute",
			doc:  `<!doctype html><html><head data-x='x>'><script>a()</script></head></html>`,
			want: "<!doctype html>" + bridge + `<html><head data-x='x>'><script>a()</script></head></html>`,
		},
		{
			name: "abruptly closed empty comment",
			doc:  "<!--><script>a()</script>-->",
			want: "<!-->" + bridge + "<script>a()</script>-->",
		},
		{
			name: "abruptly closed dash comment",
			doc:  "<!---><script>a()</script>-->",
			want: "<!--->" + bridge + "<script>a()</script>-->",
		},
		{
			name: "comment closed with bang",
			doc:  "<!-- x --!><script>a()</script>-->",
			want: "<!-- x --!>" + bridge + "<script>a()</script>-->",
		},
		{
			name: "unterminated comment",
			doc:  "<!-- <script>a()</script>",
			want: bridge + "<!-- <script>a()</script>",
		},
		{
			name: "doctype with gt in quoted identifier",
			doc:  `<!DOCTYPE html PUBLIC "a>b"><script>a()</script>`,
			want: `<!DOCTYPE html PUBLIC "a>` + bridge + `b"><script>a()</script>`,
		},
		{
			name: "script first",
			doc:  "<script>a('<head>')</script><div>x</div>",
			want: bridge + "<script>a('<head>')</script><div>x</div>",
		},
		{
			name: "plain content",
			doc:  "<div>hi</div><script>a()</script>",
			want: bridge + "<div>hi</div><script>a()</script>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(injectArtifactBridge([]byte(tt.doc), "tok", "*"))
			require.Equal(t, tt.want, got)
			assert.Equal(t, 1, strings.Count(got, bridge))
			assert.Less(t, strings.Index(got, bridge), strings.Index(strings.ToLower(got), "a("))
		})
	}
}

func TestArtifactBridgeToken(t *testing.T) {
	secret := []byte(strings.Repeat("s", artifactSecretSize))
	user1, user2 := model.NewId(), model.NewId()
	file1, file2 := model.NewId(), model.NewId()
	nonce1, nonce2 := testArtifactNonce, "zyxwvutsrqponmlkjihg-_"
	base := artifactBridgeToken(secret, user1, file1, nonce1)

	tests := []struct {
		name     string
		secret   []byte
		userID   string
		fileID   string
		nonce    string
		wantSame bool
	}{
		{name: "same user, file and nonce is deterministic", secret: secret, userID: user1, fileID: file1, nonce: nonce1, wantSame: true},
		{name: "different user", secret: secret, userID: user2, fileID: file1, nonce: nonce1},
		{name: "different file", secret: secret, userID: user1, fileID: file2, nonce: nonce1},
		{name: "different nonce", secret: secret, userID: user1, fileID: file1, nonce: nonce2},
		{name: "different secret", secret: []byte(strings.Repeat("t", artifactSecretSize)), userID: user1, fileID: file1, nonce: nonce1},
		{name: "separator cannot be shifted between user and file", secret: secret, userID: user1 + "|" + file1[:1], fileID: file1[1:], nonce: nonce1},
		{name: "separator cannot be shifted between file and nonce", secret: secret, userID: user1, fileID: file1 + "|" + nonce1[:1], nonce: nonce1[1:]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := artifactBridgeToken(tt.secret, tt.userID, tt.fileID, tt.nonce)
			assert.Regexp(t, `^[A-Za-z0-9_-]{43}$`, got)
			if tt.wantSame {
				assert.Equal(t, base, got)
			} else {
				assert.NotEqual(t, base, got)
			}
		})
	}

	t.Run("v1 tokens are not valid v2 tokens", func(t *testing.T) {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte("html-artifact-bridge:v1|" + user1 + "|" + file1))
		assert.NotEqual(t, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), base)
	})
}

func TestArtifactBridgeScriptEscaping(t *testing.T) {
	tests := []struct {
		name         string
		token        string
		targetOrigin string
	}{
		{name: "plain values", token: "abc_DEF-123", targetOrigin: "https://mm.example.com"},
		{name: "script close", token: `</script><script>alert(1)</script>`, targetOrigin: `</SCRIPT><script>alert(1)</script>`},
		{name: "quote breakout", token: `";alert(1);//`, targetOrigin: `';alert(1);//"`},
		{name: "html comment and ampersand", token: "<!-- & -->", targetOrigin: "<!-- & -->"},
		{name: "line separators", token: "a\u2028b\u2029c\n", targetOrigin: "a\u2028b\u2029c\n"},
		{name: "token holding the target origin placeholder", token: artifactBridgeTargetOriginPlaceholder, targetOrigin: "*"},
		{name: "target origin holding the token placeholder", token: "tok", targetOrigin: artifactBridgeTokenPlaceholder},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := artifactBridgeScript(tt.token, tt.targetOrigin)
			inner := strings.TrimSuffix(strings.TrimPrefix(script, "<script>"), "</script>")
			assert.NotContains(t, strings.ToLower(inner), "</script")
			assert.NotContains(t, inner, "<!--")

			// Each literal decodes back to exactly its value.
			for prefix, want := range map[string]string{"const TOKEN = ": tt.token, "const TARGET_ORIGIN = ": tt.targetOrigin} {
				i := strings.Index(inner, prefix)
				require.GreaterOrEqual(t, i, 0)
				literal, _, found := strings.Cut(inner[i+len(prefix):], ";\n")
				require.True(t, found)
				assert.NotContains(t, literal, "<")
				assert.NotContains(t, literal, ">")
				assert.NotContains(t, literal, "&")
				assert.NotContains(t, literal, "\u2028")
				assert.NotContains(t, literal, "\u2029")
				var decoded string
				require.NoError(t, json.Unmarshal([]byte(literal), &decoded))
				assert.Equal(t, want, decoded)
			}
		})
	}
}

func TestHandleGetArtifactTargetOrigin(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	sessionID := model.NewId()
	fileID := model.NewId()
	postID := model.NewId()
	channelID := model.NewId()
	const htmlDoc = "<html><body><script>run()</script></body></html>"

	tests := []struct {
		name    string
		siteURL *string
		want    string
	}{
		{name: "site url with port and path", siteURL: model.NewPointer("https://mm.example.com:8443/team/sub/"), want: "https://mm.example.com:8443"},
		{name: "http site url", siteURL: model.NewPointer("http://localhost:8065"), want: "http://localhost:8065"},
		{name: "ipv6 host", siteURL: model.NewPointer("http://[::1]:8065"), want: "http://[::1]:8065"},
		{name: "userinfo, query and fragment are dropped", siteURL: model.NewPointer("https://u:p@mm.example.com/x?y=1#z"), want: "https://mm.example.com"},
		{name: "unset site url", want: "*"},
		{name: "empty site url", siteURL: model.NewPointer(""), want: "*"},
		{name: "unparsable site url", siteURL: model.NewPointer("http://[::1"), want: "*"},
		{name: "site url without scheme", siteURL: model.NewPointer("mm.example.com"), want: "*"},
		{name: "non-http scheme", siteURL: model.NewPointer("javascript://mm.example.com"), want: "*"},
		{name: "scheme without host", siteURL: model.NewPointer("https:///path"), want: "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			e.config.enableHTMLArtifacts = true
			e.api.artifactSecretCache = []byte(strings.Repeat("k", artifactSecretSize))

			cfg := &model.Config{}
			cfg.ServiceSettings.SiteURL = tt.siteURL
			m := mocks.NewMockClient(t)
			m.EXPECT().HasPermissionToFileAction(sessionID, fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true).Maybe()
			m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, PostId: postID, ChannelId: channelID, Name: "a.html", Extension: "html", Size: int64(len(htmlDoc))}, nil)
			m.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID, FileIds: model.StringArray{fileID}}, nil)
			m.EXPECT().HasPermissionToChannel(testUserID, channelID, model.PermissionReadChannel).Return(true)
			m.EXPECT().GetConfig().Return(cfg)
			m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader(htmlDoc)), nil)
			e.api.mmClient = m

			req := httptest.NewRequest(http.MethodGet, "/artifacts/"+fileID+"?n="+testArtifactNonce, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			req.Header.Set("Sec-Fetch-Dest", "iframe")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			literal, err := json.Marshal(tt.want)
			require.NoError(t, err)
			assert.Contains(t, rec.Body.String(), "const TARGET_ORIGIN = "+string(literal)+";\n")
			assert.NotContains(t, rec.Body.String(), artifactBridgeTargetOriginPlaceholder)
		})
	}
}

func TestArtifactPermissionsPolicy(t *testing.T) {
	denied := map[string]bool{}
	for entry := range strings.SplitSeq(artifactPermissionsPolicy, ", ") {
		feature, allowlist, found := strings.Cut(entry, "=")
		require.True(t, found, entry)
		require.Regexp(t, `^[a-z][a-z-]*[a-z]$`, feature)
		require.Equal(t, "()", allowlist, feature)
		require.False(t, denied[feature], "duplicate %s", feature)
		denied[feature] = true
	}

	tests := []struct {
		name     string
		features []string
	}{
		{name: "devices and sensors", features: []string{"camera", "microphone", "geolocation", "usb", "serial", "hid", "bluetooth", "midi", "accelerometer", "gyroscope", "magnetometer"}},
		{name: "allowed by default in opaque frames", features: []string{"sync-xhr", "autoplay", "encrypted-media", "picture-in-picture", "fullscreen", "unload"}},
		{name: "clipboard and screen", features: []string{"clipboard-read", "clipboard-write", "display-capture", "window-management", "screen-wake-lock"}},
		{name: "credentials and payments", features: []string{"publickey-credentials-get", "publickey-credentials-create", "identity-credentials-get", "otp-credentials", "payment", "storage-access"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, feature := range tt.features {
				assert.True(t, denied[feature], feature)
			}
		})
	}
}

func TestArtifactSecret(t *testing.T) {
	stored := []byte(strings.Repeat("x", artifactSecretSize))

	tests := []struct {
		name string
		// existing is the stored secret before the first call (nil: none).
		existing []byte
		// raced is what another node stored between our Get and Set.
		raced []byte
	}{
		{name: "existing secret is reused", existing: stored},
		{name: "secret is created once when absent"},
		{name: "concurrently created secret wins", raced: stored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			var kv []byte
			if tt.existing != nil {
				kv = append([]byte(nil), tt.existing...)
			}
			sets := 0
			e.mockAPI.On("KVGet", artifactSecretKVKey).Return(func(string) []byte { return kv }, func(string) *model.AppError { return nil })
			e.mockAPI.On("KVSetWithOptions", artifactSecretKVKey, mock.Anything, mock.Anything).Return(
				func(_ string, value []byte, opts model.PluginKVSetOptions) bool {
					sets++
					require.True(t, opts.Atomic)
					require.Nil(t, opts.OldValue)
					if tt.raced != nil {
						kv = append([]byte(nil), tt.raced...)
						return false
					}
					if kv != nil {
						return false
					}
					kv = append([]byte(nil), value...)
					return true
				}, func(string, []byte, model.PluginKVSetOptions) *model.AppError { return nil }).Maybe()

			first, err := e.api.artifactSecret()
			require.NoError(t, err)
			second, err := e.api.artifactSecret()
			require.NoError(t, err)

			assert.Len(t, first, artifactSecretSize)
			assert.Equal(t, first, second)
			assert.Equal(t, kv, first)
			if tt.existing != nil {
				assert.Equal(t, 0, sets)
			} else {
				assert.Equal(t, 1, sets)
			}
			if tt.existing != nil || tt.raced != nil {
				assert.Equal(t, stored, first)
			}
		})
	}
}
