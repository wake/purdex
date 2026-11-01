user: Use Bash to run `for i in 1 2 3 4 5 6; do echo step $i; sleep 4; done` and then reply 'done one'.

<tool Bash | for i in 1 2 3 4 5 6; do echo step $i; sleep 4; done>

user: After you finish, also reply with the word QUEUEDTWO.

agent: done one

QUEUEDTWO

user: Use Bash to run `for i in 1 2 3 4 5 6; do echo again $i; sleep 4; done` and then reply 'done three'.

<tool Bash | for i in 1 2 3 4 5 6; do echo again $i; sleep 4; done>

agent: done three

user: Also reply with the word QUEUEDFOUR when that is done.

agent: done three

QUEUEDFOUR

<slash | /exit>
