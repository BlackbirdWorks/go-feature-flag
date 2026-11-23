# Migration: removal of `ffuser` and the `user` request field

The long-deprecated user model has been removed.

## Removed

- The Go package `ffuser` (`ffuser.NewUser`, `ffuser.NewAnonymousUser`, `ffuser.NewUserBuilder`).
  Use `ffcontext.NewEvaluationContext`, `ffcontext.NewAnonymousEvaluationContext` and
  `ffcontext.NewEvaluationContextBuilder` instead.
- The legacy `user` field (`key`, `anonymous`, `custom`) in the bodies of the relay proxy endpoints
  `POST /v1/feature/{flagKey}/eval` and `POST /v1/allflags`.

## What to send instead

```json
{
  "evaluationContext": {
    "key": "a20b1cd5-7165-4e02-a279-c0c8b90a8912",
    "custom": {
      "anonymous": false,
      "email": "john.doe@example.com"
    }
  }
}
```

`user.key` becomes `evaluationContext.key`, and `user.custom` becomes `evaluationContext.custom`.
The old `user.anonymous` flag is no longer a dedicated field: if your targeting rules use `anonymous`,
send it inside `custom`. OpenFeature SDKs using OFREP or the GO Feature Flag providers already send an
evaluation context and are not affected.

## Behaviour of a request that still sends `user`

The relay proxy answers `400 Bad Request` with the message
`assertRequest: the 'user' field is no longer supported, send an 'evaluationContext' instead`.
The request is never evaluated with an empty context.
