// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

library AssetTransfer {
    address private constant SATOSHINET_ASSET =
        address(0x0000000000000000000000000000000000534E01);

    function balanceOf(address owner, string memory assetName) internal view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("balanceOf(address,string)", owner, assetName)
        );
        require(ok, "asset balance failed");
        return string(readRawDynamicBytes(ret));
    }

    function fundingAssetAmount(string memory assetName) internal view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("fundingAssetAmount(string)", assetName)
        );
        require(ok, "funding asset failed");
        return string(readRawDynamicBytes(ret));
    }

    function claimFundingAsset(string memory assetName, string memory amount) internal {
        require(isPositive(amount), "claim amount required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature("claimFundingAsset(string,string)", assetName, amount)
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "claim funding failed");
    }

    function fundingSats() internal view returns (uint256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("fundingSats()")
        );
        require(ok, "funding sats failed");
        return abi.decode(ret, (uint256));
    }

    function transferAsset(string memory assetName, string memory recipient, string memory amount) internal {
        require(compareAmount(amount, "0") > 0, "amount required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature(
                "transferAsset(string,string,string,bytes)",
                assetName,
                recipient,
                amount,
                ""
            )
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "asset transfer failed");
    }

    function transferAssets(
        string[] memory assetNames,
        string[] memory recipients,
        string[] memory amounts,
        bytes[] memory extraData
    ) internal {
        require(
            assetNames.length == recipients.length &&
            assetNames.length == amounts.length &&
            assetNames.length == extraData.length,
            "transfer arrays"
        );
        require(assetNames.length > 0, "transfer required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature(
                "transferAssets(string[],string[],string[],bytes[])",
                assetNames,
                recipients,
                amounts,
                extraData
            )
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "asset batch transfer failed");
    }

    function compareAmount(string memory left, string memory right) internal view returns (int256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("compareAmount(string,string)", left, right)
        );
        require(ok, "amount compare failed");
        return abi.decode(ret, (int256));
    }

    function addAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("addAmount(string,string)", left, right);
    }

    function subAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("subAmount(string,string)", left, right);
    }

    function mulAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("mulAmount(string,string)", left, right);
    }

    function divAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("divAmount(string,string)", left, right);
    }

    function amountOp(string memory signature, string memory left, string memory right) private view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature(signature, left, right)
        );
        require(ok, "amount op failed");
        return string(readRawDynamicBytes(ret));
    }

    function readRawDynamicBytes(bytes memory data) internal pure returns (bytes memory) {
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

    function uintToString(uint256 value) internal pure returns (string memory) {
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

    function isPositive(string memory value) internal view returns (bool) {
        return compareAmount(value, "0") > 0;
    }

    function stringToUintFloor(string memory value) internal pure returns (uint256) {
        bytes memory data = bytes(value);
        require(data.length != 0, "empty amount");
        uint256 result;
        for (uint256 i = 0; i < data.length; i++) {
            bytes1 ch = data[i];
            if (ch == ".") {
                return result;
            }
            require(ch >= "0" && ch <= "9", "invalid amount");
            result = result * 10 + (uint8(ch) - 48);
        }
        return result;
    }
}

library ContractMetadataAssets {
    function emptyManagedAssets() internal pure returns (string[] memory assets) {
        assets = new string[](0);
    }

    function singleManagedAsset(string memory assetName) internal pure returns (string[] memory assets) {
        assets = new string[](1);
        assets[0] = assetName;
    }

    function pairManagedAssets(string memory assetA, string memory assetB) internal pure returns (string[] memory assets) {
        assets = new string[](2);
        assets[0] = assetA;
        assets[1] = assetB;
    }
}

interface ISatoshiNetContractInfo {
    function contractName() external view returns (string memory);
    function contractSubtype() external view returns (string memory);
    function managedAssetCount() external view returns (uint256);
    function managedAsset(uint256 index) external view returns (string memory);
    function managedAssetBalance(uint256 index) external view returns (string memory);
    function managedAssetBalance(string calldata assetName) external view returns (string memory);
}

abstract contract SatoshiNetContractInfo is ISatoshiNetContractInfo {
    string private _contractName;
    string private _contractSubtype;
    string[] private _managedAssets;

    constructor(string memory name_, string memory subtype_, string[] memory managedAssets_) {
        _contractName = name_;
        _contractSubtype = subtype_;
        for (uint256 i = 0; i < managedAssets_.length; i++) {
            _managedAssets.push(managedAssets_[i]);
        }
    }

    function contractName() external view returns (string memory) {
        return _contractName;
    }

    function contractSubtype() external view returns (string memory) {
        return _contractSubtype;
    }

    function managedAssetCount() external view returns (uint256) {
        return _managedAssets.length;
    }

    function managedAsset(uint256 index) external view returns (string memory) {
        require(index < _managedAssets.length, "asset index");
        return _managedAssets[index];
    }

    function managedAssetBalance(uint256 index) external view returns (string memory) {
        require(index < _managedAssets.length, "asset index");
        return _managedAssetBalance(_managedAssets[index]);
    }

    function managedAssetBalance(string calldata assetName) external view returns (string memory) {
        return _managedAssetBalance(assetName);
    }

    function _managedAssetBalance(string memory assetName) internal view virtual returns (string memory) {
        return AssetTransfer.balanceOf(address(this), assetName);
    }
}

contract Escrow is SatoshiNetContractInfo {
    address public immutable owner;
    string public ownerRecipient;
    string public assetName;
    uint256 public released;

    constructor(string memory assetName_, address owner_, string memory ownerRecipient_)
        SatoshiNetContractInfo("escrow", "escrow", ContractMetadataAssets.singleManagedAsset(assetName_))
    {
        require(bytes(assetName_).length != 0, "asset required");
        require(owner_ != address(0), "owner required");
        require(bytes(ownerRecipient_).length != 0, "owner recipient required");
        assetName = assetName_;
        owner = owner_;
        ownerRecipient = ownerRecipient_;
    }

    receive() external payable {
        _depositDefault();
    }

    fallback() external payable {
        _depositDefault();
    }

    function _depositDefault() private {
        string memory amountText = AssetTransfer.fundingAssetAmount(assetName);
        require(AssetTransfer.isPositive(amountText), "default deposit required");
        AssetTransfer.claimFundingAsset(assetName, amountText);
    }

    function release(string calldata recipient, uint256 amount) external returns (bool) {
        require(msg.sender == owner, "only owner");
        require(bytes(recipient).length != 0, "recipient required");
        require(amount > 0, "amount required");
        string memory amountText = AssetTransfer.uintToString(amount);
        require(AssetTransfer.compareAmount(AssetTransfer.balanceOf(address(this), assetName), amountText) >= 0, "insufficient asset");
        released += amount;
        AssetTransfer.transferAsset(assetName, recipient, amountText);
        return true;
    }

    function close() external returns (bool) {
        string memory remaining = AssetTransfer.balanceOf(address(this), assetName);
        if (AssetTransfer.isPositive(remaining)) {
            string[] memory assetNames = new string[](1);
            string[] memory recipients = new string[](1);
            string[] memory amounts = new string[](1);
            bytes[] memory extraData = new bytes[](1);
            assetNames[0] = assetName;
            recipients[0] = ownerRecipient;
            amounts[0] = remaining;
            AssetTransfer.transferAssets(assetNames, recipients, amounts, extraData);
        }
        return true;
    }
}

contract Crowdfund is SatoshiNetContractInfo {
    address public immutable owner;
    string public assetName;
    uint256 public immutable target;
    uint256 public totalPledged;
    bool public claimed;

    constructor(string memory assetName_, uint256 target_, address owner_)
        SatoshiNetContractInfo("crowdfund", "crowdfund", ContractMetadataAssets.singleManagedAsset(assetName_))
    {
        require(bytes(assetName_).length != 0, "asset required");
        require(target_ > 0, "target required");
        require(owner_ != address(0), "owner required");
        assetName = assetName_;
        target = target_;
        owner = owner_;
    }

    receive() external payable {
        pledge();
    }

    fallback() external payable {
        pledge();
    }

    function pledge() public returns (bool) {
        string memory amountText = AssetTransfer.fundingAssetAmount(assetName);
        uint256 amount = AssetTransfer.stringToUintFloor(amountText);
        require(amount > 0, "amount required");
        AssetTransfer.claimFundingAsset(assetName, amountText);
        require(AssetTransfer.compareAmount(AssetTransfer.balanceOf(address(this), assetName), AssetTransfer.uintToString(totalPledged + amount)) >= 0, "unfunded pledge");
        totalPledged += amount;
        return true;
    }

    function claim(string calldata beneficiary) external returns (bool) {
        require(msg.sender == owner, "only owner");
        require(!claimed, "claimed");
        require(totalPledged >= target, "target not reached");
        require(bytes(beneficiary).length != 0, "beneficiary required");
        string memory amountText = AssetTransfer.uintToString(totalPledged);
        require(AssetTransfer.compareAmount(AssetTransfer.balanceOf(address(this), assetName), amountText) >= 0, "insufficient asset");
        claimed = true;
        AssetTransfer.transferAsset(assetName, beneficiary, amountText);
        return true;
    }

    function close() external returns (bool) {
        return true;
    }
}

// InternalERC20 is an EVM-only ledger sample. Deploying this contract does not
// issue a SatoshiNet/ORDX/BRC20/Runes ticker, and these balances are not UTXO
// assets. Wallets and indexers must treat this as contract-local state only.
contract InternalERC20 is SatoshiNetContractInfo {
    string public name;
    string public symbol;
    uint8 public immutable decimals;
    uint256 public totalSupply;
    address public immutable owner;

    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    event Transfer(address indexed from, address indexed to, uint256 amount);
    event Approval(address indexed owner, address indexed spender, uint256 amount);

    constructor(string memory name_, string memory symbol_, uint8 decimals_)
        SatoshiNetContractInfo("erc20", "erc20", ContractMetadataAssets.emptyManagedAssets())
    {
        require(bytes(name_).length != 0, "name required");
        require(bytes(symbol_).length != 0, "symbol required");
        name = name_;
        symbol = symbol_;
        decimals = decimals_;
        owner = msg.sender;
    }

    modifier onlyOwner() {
        require(msg.sender == owner, "only owner");
        _;
    }

    function mint(address to, uint256 amount) external onlyOwner returns (bool) {
        require(to != address(0), "zero address");
        require(amount > 0, "amount required");
        totalSupply += amount;
        balanceOf[to] += amount;
        emit Transfer(address(0), to, amount);
        return true;
    }

    function burn(uint256 amount) external returns (bool) {
        require(amount > 0, "amount required");
        require(balanceOf[msg.sender] >= amount, "insufficient balance");
        balanceOf[msg.sender] -= amount;
        totalSupply -= amount;
        emit Transfer(msg.sender, address(0), amount);
        return true;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        _transfer(msg.sender, to, amount);
        return true;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        require(spender != address(0), "zero address");
        allowance[msg.sender][spender] = amount;
        emit Approval(msg.sender, spender, amount);
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        uint256 currentAllowance = allowance[from][msg.sender];
        require(currentAllowance >= amount, "allowance exceeded");
        allowance[from][msg.sender] = currentAllowance - amount;
        emit Approval(from, msg.sender, allowance[from][msg.sender]);
        _transfer(from, to, amount);
        return true;
    }

    function assertBalance(address account, uint256 expected) external view returns (bool) {
        require(balanceOf[account] == expected, "unexpected balance");
        return true;
    }

    function assertAllowance(address tokenOwner, address spender, uint256 expected) external view returns (bool) {
        require(allowance[tokenOwner][spender] == expected, "unexpected allowance");
        return true;
    }

    function assertTotalSupply(uint256 expected) external view returns (bool) {
        require(totalSupply == expected, "unexpected supply");
        return true;
    }

    function close() external returns (bool) {
        return true;
    }

    function _transfer(address from, address to, uint256 amount) private {
        require(to != address(0), "zero address");
        require(amount > 0, "amount required");
        require(balanceOf[from] >= amount, "insufficient balance");
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
        emit Transfer(from, to, amount);
    }
}
