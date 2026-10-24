package remotenode

// KeyLinePrefix starts the stdout line on which the node prints its public key.
// The controller reads the key from the launch it started, never from the
// channel, and pins it.
const KeyLinePrefix = "satelle-node-key "

// NodeRoot is the default working root on the node, as the node's shell spells it.
const NodeRoot = "~/satelle-node"
