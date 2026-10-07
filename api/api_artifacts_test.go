// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
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
	wantToken := artifactBridgeToken(secret, testUserID, fileID)

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

				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Mattermost-User-Id", testUserID)
				req.Header.Set("Sec-Fetch-Dest", "iframe")
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
				assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
				assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Resource-Policy"))
				assert.Equal(t, "inline", rec.Header().Get("Content-Disposition"))
				assert.Equal(t, string(injectArtifactBridge([]byte(htmlDoc), wantToken)), rec.Body.String())
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
	wantToken := artifactBridgeToken(secret, testUserID, fileID)

	tests := []struct {
		name      string
		dest      string
		wantServe bool
	}{
		{name: "missing header shows interstitial"},
		{name: "document shows interstitial", dest: "document"},
		{name: "empty shows interstitial", dest: "empty"},
		{name: "iframe serves artifact", dest: "iframe", wantServe: true},
		{name: "iframe is case-insensitive", dest: "IFrame", wantServe: true},
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

			req := httptest.NewRequest(http.MethodGet, "/artifacts/"+fileID, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			if tt.dest != "" {
				req.Header.Set("Sec-Fetch-Dest", tt.dest)
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

func TestInjectArtifactBridge(t *testing.T) {
	bridge := artifactBridgeScript("tok")

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
			got := string(injectArtifactBridge([]byte(tt.doc), "tok"))
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
	base := artifactBridgeToken(secret, user1, file1)

	tests := []struct {
		name     string
		secret   []byte
		userID   string
		fileID   string
		wantSame bool
	}{
		{name: "same user and file is deterministic", secret: secret, userID: user1, fileID: file1, wantSame: true},
		{name: "different user", secret: secret, userID: user2, fileID: file1},
		{name: "different file", secret: secret, userID: user1, fileID: file2},
		{name: "different secret", secret: []byte(strings.Repeat("t", artifactSecretSize)), userID: user1, fileID: file1},
		{name: "separator cannot be shifted between fields", secret: secret, userID: user1 + "|" + file1[:1], fileID: file1[1:]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := artifactBridgeToken(tt.secret, tt.userID, tt.fileID)
			assert.Regexp(t, `^[A-Za-z0-9_-]{43}$`, got)
			if tt.wantSame {
				assert.Equal(t, base, got)
			} else {
				assert.NotEqual(t, base, got)
			}
		})
	}
}

func TestArtifactBridgeScriptEscaping(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{name: "plain token", token: "abc_DEF-123"},
		{name: "script close", token: `</script><script>alert(1)</script>`},
		{name: "quote breakout", token: `";alert(1);//`},
		{name: "html comment and ampersand", token: "<!-- & -->"},
		{name: "line separators", token: "a\u2028b\u2029c\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := artifactBridgeScript(tt.token)
			inner := strings.TrimSuffix(strings.TrimPrefix(script, "<script>"), "</script>")
			assert.NotContains(t, strings.ToLower(inner), "</script")
			assert.NotContains(t, inner, "<!--")
			assert.NotContains(t, inner, artifactBridgeTokenPlaceholder)

			// The literal decodes back to exactly the token.
			const prefix = "const TOKEN = "
			i := strings.Index(inner, prefix)
			require.GreaterOrEqual(t, i, 0)
			rest := inner[i+len(prefix):]
			literal, _, found := strings.Cut(rest, ";\n")
			require.True(t, found)
			assert.NotContains(t, literal, "<")
			assert.NotContains(t, literal, ">")
			assert.NotContains(t, literal, "&")
			assert.NotContains(t, literal, "\u2028")
			assert.NotContains(t, literal, "\u2029")
			var decoded string
			require.NoError(t, json.Unmarshal([]byte(literal), &decoded))
			assert.Equal(t, tt.token, decoded)
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
