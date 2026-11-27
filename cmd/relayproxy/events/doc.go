// Package events holds the runtime-level exporters, the event channel and the notifiers of the relay proxy.
//
// Exporters and notifiers are resources of the whole runtime, not of a flag set: M exporters shared by N flag
// sets are M running pipelines, and the number of goroutines and the memory do not depend on N. An evaluation
// hands its event to [Dispatcher.Collect], which does one non-blocking send into one bounded channel. A small
// fixed pool of workers moves the events from the channel to the exporter managers. When the channel is full
// the event is dropped and counted, never waited for.
//
// The [Notifications] worker consumes the changes of the reconcile engine and hands each of them to every
// notifier through a bounded queue per notifier, so a slow notifier delays only itself.
//
// [Builder] turns the exporters and notifiers sections of the bootstrap file (config v2) into engine exporters
// and notifiers. AWS backed kinds take their client or configuration from the connection factory by connection
// name. [Runtime] ties the dispatcher and the notification worker together under one lifecycle (start, reload in
// place, shutdown with a drain deadline), and [Collector] exposes [Status] to Prometheus.
//
// The package is built beside the running relay: nothing in the relay reads it yet. The relay keeps running on
// the version 1 configuration until the switch to config v2.
package events
