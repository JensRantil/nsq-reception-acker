`nsq-reception-acker`
=====================
Similar to [`nsq_to_nsq`](http://nsq.io/components/utilities.html#nsq_to_nsq),
but also published an acknowledgement to a topic that a message has been
received.

Usecase
-------
Imagine you have an elastic environment where NSQ message loss isn't
acceptable. NSQ doesn't come with any message replication so you need to build
around that yourself.

One way to replicate messages is to have the publisher connect to two different
`nsqd`s and publish each message to both of them. There are two problems with
this approach: 1) It doesn't use the NSQ best-practise of always publishing to
`localhost` `nsqd` and 2) it requires hardcoding addresses to `nsqd`s in the
publisher which means we are not using `nsqlookupd` for discovery.

Instead, the approach that `nsq-reception-acker` takes is that messages are
published once to a topic on `localhost` and `nsq-reception-acker`, running on
a separate VM than the publisher, is consuming messages from that topic. For
each message it consumes, it deserializes its envelope (see below) and 1)
forwards the actual payload (`payload`) to `payload-destination` and 2) also
triggers an acknowledgement message (`message-id`) being sent to a separate
topic (`acknowledgement-topic`). Finally, the initial publisher is subscribed
to `acknowledgement-topic` and to make sure that `N` numbers of
`nsq-reception-acker` have received a copy of the original message.

Envelope Message Format
-----------------------
Each message sent to `nsq-reception-acker` must be JSON formatted and contain
the following fields:

 * *`payload-destination`*: The topic to which the `payload` string should be
   sent to.
 * *`payload`*: The message body to be sent to `payload-destination`.
 * *`acknowledgement-topic`*: The topic to which the acknowledgement should be
   sent.
 * *`message-id`*: The message body that should be sent to the
   `acknowledgement-topic`.
