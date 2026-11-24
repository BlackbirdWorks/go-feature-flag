# Evaluation Context

## Overview
All Semaphore SDKs use an evaluation context to determine which variation of a feature flag to serve.

An evaluation context in a feature flagging system is crucial for determining the output of a feature flag evaluation.

It's a collection of pertinent data about the conditions under which the evaluation is being made.
This data can be supplied through a mix of static information _(server name, IP, etc ...)_ and dynamic inputs
_(information about the user performing the action, etc ...)_, along with state information that is implicitly carried
through the execution of the program.

## About contexts
Semaphore evaluation contexts are data objects representing users, devices, organizations, and other entities that are used to determine which variation of a feature flag to serve. 

The context is used to evaluate the [targeting queries](../configure-flags/target-with-flags.md) and determine which variation of a feature flag to serve to a user.

> [!NOTE]
> **Only the evaluation context attributes you provide are available for targeting queries.**
>
> If you want to use a specific attribute in your targeting queries, you must include it in the evaluation context.
> If not present the query will not apply.

## Example context: Mike Wazowski at Monsters, Inc.

As an example, let's assume Mike Wazowski is one of your end users. He is a scare assistant who works on the scare floor at Monsters, Inc with James P. Sullivan.   
Mike has two mobile devices, an Android phone and an iPad tablet. Mike uses your application on both devices as part of his work.

Given this information, you may know the following things about Mike Wazowski:

- his name, email and job title _("Scare assistant")_
- his employee ID _(used for the targetingKey)_
- his work station position _("scare floor")_
- his organization's name _("Monsters, Inc."),_
- his device's type _("iPad")_
- his coworker's name _("James P. Sullivan")_

Here is an example of what the data structure for Mike Wazowski evaluation context object might look like:

```json
{
  "targetingKey": "34c7f8ab-6d14-4aa6-a77f-effc6245da6f",
  "firstname": "Mike",
  "lastname": "Wazowski",
  "email": "mike.wazowski@monster.inc",
  "organization": "Monsters, Inc.",
  "jobFunction": "Scare assistant",
  "location": "scare floor",
  "coworker": "James P. Sullivan",
  "device": "iPad"
}
```
## Targeting Key
> [!NOTE]
> **The targeting key requirement depends on the flag and provider.**

Percentage and progressive rollouts require a non-empty key for bucketing.
This includes rules introduced by scheduled rollout steps.
By default, the `targetingKey` supplies this value; a configured [custom bucketing key](../configure-flags/custom-bucketing.md) can supply it instead.

Static flags and attribute-based targeting without bucketing can be evaluated without a targeting key.

A **targeting key** is a unique identifier that represents the context of the evaluation _(email, session id, a fingerprint or anything that is consistent)_,
ensuring that they are consistently exposed to the same variation of a feature, even across multiple visits or sessions.

The targeting key is used to ensure that a user consistently receives the same variation of a feature flag over time.  
For instance, **Semaphore** ensures that in cases where a feature is being rolled out to a percentage of users, based on the targeting key, they will see the same variation each time they encounter the feature flag.

## Reserved properties in the evaluation context
> [!CAUTION]
> If you put a key named `semaphore` (or its legacy name `gofeatureflag`) in your evaluation context, it may break internal features of Semaphore.
> These property names are reserved for internal use.

When you create an evaluation context some fields are reserved for Semaphore.  
Those fields are used by Semaphore directly, you can use them as will in your targeting queries, but you should be aware that they are used internally for Semaphore.

| Field                            | Description                                                                                                                                                                                                                  |
|----------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `semaphore.currentDateTime`      | If this property is set, we will use this date as base for all the rollout strategies which implies dates _(experimentation, progressive and scheduled)_.<br/>**Format:** Date following the RFC3339 format.                  |
| `semaphore.flagList`             | If this property is set, in the bulk evaluation mode (for the client SDK) we will only evaluate the flags in this list.<br/>If empty or not set the default behavior is to evaluate all the flags.<br/>**Format:** []string |
| `semaphore.exporterMetadata`     | If this property is set, we will add all the fields in the feature event send to the provider.<br/>**Format:** map[string]string\|number\|bool                                                                               |

The reserved key is `semaphore`. The key `gofeatureflag`, which the GO Feature Flag providers send, is still accepted with
the same sub-keys (`gofeatureflag.flagList`, ...) and the relay proxy logs one warning when it sees it. When both keys carry
the same field, the one of `semaphore` is used.
