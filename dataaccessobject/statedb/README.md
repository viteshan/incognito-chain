# Design Spec
link: https://drive.google.com/file/d/1gI_dwf2h8irzAGefHfuX_79ASWItdqch/view?usp=sharing

## Key
key is 32 bytes length with:
- 10 bytes prefix
- 20 bytes key

## Value
value is depend on type of state object

## State Object
- Assume that hash(something) return 32 bytes value
1. All Shard Committee (deprecate)
- key: first 12 bytes of `hash(shard-committee-prefix)` with first 20 bytes of `hash(beaconheight)`
- value: 
    * temporary value: 32 bytes random ID
    * real value: all shard committee
- key -> temporary value -> real value. Beacause shard committee not change so frequently

2. Committee
Used for beacon and all shards, distinguish between shards and beacon by prefix. Each shard or beacon has different prefix value
- key: first 12 bytes of `hash(committee-shardID-prefix)` with first 20 bytes of `hash(committee-key-bytes)`
- value: committee state( shardID and Committee Key)

3. Reward Receiver
Used for reward receiver in beacon only
- key: first 12 bytes of `hash(reward-receiver-prefix)` with first 20 bytes of `hash(incognito-key-bytes)`
- value: reward receiver state( incognito public key and reward receiver payment address)