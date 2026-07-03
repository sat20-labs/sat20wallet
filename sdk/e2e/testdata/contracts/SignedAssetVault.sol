// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// 部署者预先设定一种资产和单位数量，用户只有拿到部署者签名的授权凭证，才能从合约中提取 unitAmount * n 数量的资产。每个授权 key 只能使用一次
contract SignedAssetVault {
    address private constant SATOSHINET_ASSET =
        address(0x0000000000000000000000000000000000534E01);

    address public immutable deployer;
    string public assetName;
    uint256 public immutable unitAmount;
    mapping(int64 => bool) public usedKeys;

    constructor(string memory assetName_, uint256 unitAmount_, address deployer_) {
        require(bytes(assetName_).length != 0, "asset required");
        require(unitAmount_ > 0, "unit required");
        require(deployer_ != address(0), "deployer required");
        assetName = assetName_;
        unitAmount = unitAmount_;
        deployer = deployer_;
    }

    receive() external payable {
        _depositDefault();
    }

    fallback() external payable {
        _depositDefault();
    }

    function _depositDefault() private {
        string memory amount = _fundingAssetAmount(assetName);
        require(_compareAmount(amount, "0") > 0, "default deposit required");
        _claimFundingAsset(assetName, amount);
    }

    function withdraw(uint256 n, bytes calldata authorization) external returns (bool) {
        if (n == 0) {
            return false;
        }

        (string memory recipient, int64 key, bytes memory signature) =
            abi.decode(authorization, (string, int64, bytes));
        if (usedKeys[key]) {
            return false;
        }
        bytes32 digest = keccak256(abi.encodePacked(recipient, key, n));
        if (_recover(digest, signature) != deployer) {
            return false;
        }

        string memory amount = _uintToString(unitAmount * n);
        require(_compareAmount(_balanceOf(address(this), assetName), amount) >= 0, "insufficient asset");
        usedKeys[key] = true;
        _transferAsset(assetName, recipient, amount);
        return true;
    }

    function _balanceOf(address owner, string memory name) private view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("balanceOf(address,string)", owner, name)
        );
        require(ok, "asset balance failed");
        return string(_readRawDynamicBytes(ret));
    }

    function _fundingAssetAmount(string memory name) private view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("fundingAssetAmount(string)", name)
        );
        require(ok, "funding asset failed");
        return string(_readRawDynamicBytes(ret));
    }

    function _claimFundingAsset(string memory name, string memory amount) private {
        require(_compareAmount(amount, "0") > 0, "claim amount required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature("claimFundingAsset(string,string)", name, amount)
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "claim funding failed");
    }

    function _compareAmount(string memory left, string memory right) private view returns (int256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("compareAmount(string,string)", left, right)
        );
        require(ok, "amount compare failed");
        return abi.decode(ret, (int256));
    }

    function _transferAsset(string memory name, string memory recipient, string memory amount) private {
        require(_compareAmount(amount, "0") > 0, "amount required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature(
                "transferAsset(string,string,string,bytes)",
                name,
                recipient,
                amount,
                ""
            )
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "asset transfer failed");
    }

    function _readRawDynamicBytes(bytes memory data) private pure returns (bytes memory) {
        require(data.length >= 32, "bad balance response");
        uint256 size;
        assembly {
            size := mload(add(data, 32))
        }
        require(data.length >= 32 + size, "short balance response");
        bytes memory out = new bytes(size);
        for (uint256 i = 0; i < size; i++) {
            out[i] = data[32 + i];
        }
        return out;
    }

    function _recover(bytes32 digest, bytes memory signature) private pure returns (address) {
        require(signature.length == 65, "bad signature length");
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := mload(add(signature, 32))
            s := mload(add(signature, 64))
            v := byte(0, mload(add(signature, 96)))
        }
        if (v < 27) {
            v += 27;
        }
        require(v == 27 || v == 28, "bad recovery id");
        return ecrecover(digest, v, r, s);
    }

    function _uintToString(uint256 value) private pure returns (string memory) {
        if (value == 0) {
            return "0";
        }
        uint256 temp = value;
        uint256 digits;
        while (temp != 0) {
            digits++;
            temp /= 10;
        }
        bytes memory buffer = new bytes(digits);
        while (value != 0) {
            digits -= 1;
            buffer[digits] = bytes1(uint8(48 + uint256(value % 10)));
            value /= 10;
        }
        return string(buffer);
    }
}
