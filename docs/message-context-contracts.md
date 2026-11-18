# Context contracts

The CLI reads the selected core source, including local `replace` directives, when generating message handlers. Unreleased source changes can share a version with published artifacts, so a version alone does not select the new message contexts.

| Transport | Context | Generated registration |
| --- | --- | --- |
| HTTP | `spine.Ctx` | Controller `RegisterRoutes` callbacks |
| WebSocket | `spine.WebSocketCtx` | Controller `RegisterRoutes` with `r.WS` and `input.Message[T]` |
| Event delivery | `spine.EventCtx` | Consumer method expression |

Here `spine` refers to `github.com/NARUBROWN/spine/pkg/spine`.

HTTP handlers use `ctx.ExecutionContext()` for the standard context and request-local Get/Set. Request and response views expose the public HTTP APIs. The CLI does not generate dependencies on the internal execution owner.

WebSocket handlers receive a frame context. Generated controllers inject `ws.Sender`; replies use `sender.Send(ctx, connectionID.Value, ws.TextMessage, payload)`. Event consumers receive a delivery context. The runtime owns frame completion and event ACK/NACK.

Contract inspection resolves import aliases and type aliases. It rejects HTTP or event contexts in WebSocket callbacks, message contexts in HTTP callbacks, and incompatible contexts in event consumers when the selected core provides `EventCtx`. Standard `context.Context` remains supported for WebSocket handlers.

For older cores without the new message contexts, generation preserves the existing context and registration forms. Generated message handlers return an explicit unimplemented error until application logic is supplied.

The application-provided synchronous `publish.Publisher` remains available to DI. A user constructor returning that reserved interface is rejected.

## Validation

Run the regular suite and static checks:

```sh
go test -race ./...
go vet ./...
```

To test the new contracts against a local core source candidate:

```sh
SPINE_CONTROLLER_CORE_SOURCE=/absolute/path/to/spine \
  go test ./internal/cli \
  -run 'TestMessageContextCandidateEndToEnd|TestControllerCandidateEndToEnd|TestPreviewLifecycleCandidateEndToEnd' \
  -count=1 -timeout=180s
```

The message candidate test covers both project layouts, registered generated modules, HTTP public-view access, real local WebSocket send/receive, and event ACK/NACK through an in-memory adapter. It does not verify a real broker or a published installation of the unreleased core.
