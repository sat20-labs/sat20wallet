// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// Test fixture only. The SDK compiles and deploys this contract to isolated
// SatoshiNet nodes; none of the assertions substitute for execution/settlement.
contract SDKReviewProbe {
    address private constant ASSET = address(0x534E01);
    address public immutable owner;
    string public assetA;
    string public assetB;
    uint256 public counter;
    uint256 public depositedA;
    uint256 public depositedB;

    constructor(string memory a, string memory b) payable {
        owner = msg.sender;
        assetA = a;
        assetB = b;
    }

    function contractName() external pure returns (string memory) { return "SDKReviewProbe"; }
    function contractSubtype() external pure returns (string memory) { return "sdk-review"; }
    function managedAssetCount() external pure returns (uint256) { return 2; }
    function managedAsset(uint256 i) external view returns (string memory) { require(i < 2); return i == 0 ? assetA : assetB; }
    function stateView() external view returns (string memory) {
        return string(abi.encodePacked('{"counter":', decimal(counter), ',"depositedA":', decimal(depositedA), ',"depositedB":', decimal(depositedB), '}'));
    }
    function inc() external { counter++; }
    function setThenRevert() external payable { counter = 999; revert("intentional rollback"); }
    function nestedRevertCaught() external {
        (bool ok,) = address(this).call(abi.encodeWithSignature("setThenRevert()"));
        require(!ok, "nested revert missing"); counter++;
    }
    function staticWriteRejected() external {
        (bool ok,) = address(this).staticcall(abi.encodeWithSignature("inc()"));
        require(!ok, "STATICCALL wrote state"); counter++;
    }
    function burnGas() external { while (true) { counter++; } }
    function deposit() public payable {
        depositedA += claim(assetA);
        depositedB += claim(assetB);
    }
    receive() external payable { deposit(); }
    fallback() external payable { revert("unknown selector"); }
    function pay(string calldata recipient, string calldata amount, bool undo) external {
        require(msg.sender == owner, "only owner");
        (bool ok, bytes memory ret) = ASSET.call(abi.encodeWithSignature(
            "transferAsset(string,string,string,bytes)", assetA, recipient, amount, bytes("")));
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "transfer failed");
        counter++;
        require(!undo, "rollback asset intent");
    }
    // User settlement is deliberately empty; the framework distributes surplus.
    function close() external pure returns (bool) { return true; }

    function claim(string memory name) private returns (uint256) {
        (bool ok, bytes memory ret) = ASSET.staticcall(abi.encodeWithSignature("fundingAssetAmount(string)", name));
        require(ok && ret.length >= 32, "funding query");
        uint256 n;
        assembly { n := mload(add(ret, 32)) }
        require(n <= ret.length - 32, "funding response");
        bytes memory text = new bytes(n);
        uint256 amount;
        for (uint256 i; i < n; i++) {
            text[i] = ret[32 + i];
            require(text[i] >= "0" && text[i] <= "9", "integer fixture amount");
            amount = amount * 10 + uint8(text[i]) - 48;
        }
        if (amount != 0) {
            (ok, ret) = ASSET.call(abi.encodeWithSignature("claimFundingAsset(string,string)", name, string(text)));
            require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "claim failed");
        }
        return amount;
    }
    function decimal(uint256 n) private pure returns (string memory) {
        if (n == 0) return "0";
        uint256 m = n; uint256 size;
        while (m != 0) { m /= 10; size++; }
        bytes memory out = new bytes(size);
        while (n != 0) { out[--size] = bytes1(uint8(48 + n % 10)); n /= 10; }
        return string(out);
    }
}

contract SDKReviewRevertingConstructor {
    constructor() payable { revert("intentional constructor rollback"); }
}
